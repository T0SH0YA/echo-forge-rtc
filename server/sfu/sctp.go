// SCTP sobre DTLS — backbone do WebRTC DataChannel (RFC 8261/8831).
//
// O SFU é DTLS server; quando o handshake termina, levantamos uma
// associação SCTP sobre o mesmo `*dtls.Conn` (que já implementa net.Conn).
// Cada stream SCTP = um DataChannel; abertura é sinalizada por DCEP
// (RFC 8832): mensagem PPID 50 com tipo `DATA_CHANNEL_OPEN` (0x03) do
// peer, respondemos `DATA_CHANNEL_ACK` (0x02).
//
// Forwarding 1→N: mensagens recebidas num stream são reenviadas pra
// todos os outros peers da sala usando o stream com mesmo label (criado
// sob demanda).
//
// Motor: pion/sctp (MIT). O wrapping de DCEP, o forwarding e o mapping
// label→stream são código nosso.
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"sync"
	"sync/atomic"

	"github.com/pion/logging"
	"github.com/pion/sctp"
)

const (
	// PPIDs definidos em RFC 8831 §8
	PPIDDCEP        uint32 = 50
	PPIDStringUTF8  uint32 = 51
	PPIDBinary      uint32 = 53
	PPIDStringEmpty uint32 = 56
	PPIDBinaryEmpty uint32 = 57

	// DCEP message types (RFC 8832 §5)
	DCEPMsgAck  byte = 0x02
	DCEPMsgOpen byte = 0x03
)

var (
	sctpAssoc atomic.Uint64 // associações SCTP estabelecidas
	dcChans   atomic.Uint64 // DataChannels abertos
	dcMsgIn   atomic.Uint64
	dcMsgFwd  atomic.Uint64
)

// DCEPOpen — payload do DATA_CHANNEL_OPEN (parseado).
type DCEPOpen struct {
	ChannelType          byte
	Priority             uint16
	ReliabilityParameter uint32
	Label                string
	Protocol             string
}

// ParseDCEPOpen decodifica o payload (RFC 8832 §5.1):
//
//	0      1      2      3
//	+------+------+------+------+
//	| Msg=3| ChTyp|   Priority  |
//	+------+------+------+------+
//	|     Reliability Param     |
//	+------+------+------+------+
//	| LabelLen   |  ProtoLen    |
//	+------+------+------+------+
//	| Label …                   |
//	| Protocol …                |
//	+---------------------------+
func ParseDCEPOpen(b []byte) (*DCEPOpen, error) {
	if len(b) < 12 || b[0] != DCEPMsgOpen {
		return nil, errors.New("dcep: not OPEN")
	}
	labelLen := int(binary.BigEndian.Uint16(b[8:10]))
	protoLen := int(binary.BigEndian.Uint16(b[10:12]))
	if 12+labelLen+protoLen > len(b) {
		return nil, errors.New("dcep: truncated")
	}
	return &DCEPOpen{
		ChannelType:          b[1],
		Priority:             binary.BigEndian.Uint16(b[2:4]),
		ReliabilityParameter: binary.BigEndian.Uint32(b[4:8]),
		Label:                string(b[12 : 12+labelLen]),
		Protocol:             string(b[12+labelLen : 12+labelLen+protoLen]),
	}, nil
}

// BuildDCEPAck — 1 byte com tipo 0x02.
func BuildDCEPAck() []byte { return []byte{DCEPMsgAck} }

// dataChannel = um stream SCTP já aberto + sua etiqueta.
type dataChannel struct {
	stream *sctp.Stream
	label  string
}

// SCTPState — estado da associação no Session.
type sctpState struct {
	assoc    *sctp.Association
	mu       sync.Mutex
	channels map[string]*dataChannel // label → channel
}

func (s *Session) initSCTPState() *sctpState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sctp == nil {
		s.sctp = &sctpState{channels: map[string]*dataChannel{}}
	}
	return s.sctp
}

// startSCTP estabelece a associação SCTP sobre o *dtls.Conn quando o
// handshake DTLS termina. O SFU é cliente SCTP — Chrome aceita ambos os
// papéis; iniciamos do nosso lado pra simplificar.
func (srv *Server) startSCTP(sess *Session) {
	sess.mu.Lock()
	dtlsConn := sess.dtlsConn
	sess.mu.Unlock()
	if dtlsConn == nil {
		return
	}
	cfg := sctp.Config{
		NetConn:       dtlsConn,
		LoggerFactory: logging.NewDefaultLoggerFactory(),
	}
	go func() {
		assoc, err := sctp.Client(cfg)
		if err != nil {
			log.Printf("[sfu] sctp client fail session=%s err=%v", sess.ID, err)
			return
		}
		sctpAssoc.Add(1)
		st := sess.initSCTPState()
		st.mu.Lock()
		st.assoc = assoc
		st.mu.Unlock()
		log.Printf("[sfu] sctp established session=%s", sess.ID)
		srv.sctpAcceptLoop(sess, assoc)
	}()
}

// sctpAcceptLoop aceita streams remotos (DataChannels iniciados pelo browser).
func (srv *Server) sctpAcceptLoop(sess *Session, assoc *sctp.Association) {
	for {
		stream, err := assoc.AcceptStream()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("[sfu] sctp accept session=%s err=%v", sess.ID, err)
			}
			return
		}
		go srv.handleSCTPStream(sess, stream)
	}
}

// handleSCTPStream lê DCEP + mensagens de aplicação de um stream e
// roteia mensagens pros outros peers.
func (srv *Server) handleSCTPStream(sess *Session, stream *sctp.Stream) {
	buf := make([]byte, 64*1024)
	var label string
	for {
		n, ppid, err := stream.ReadSCTP(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("[sfu] sctp read session=%s stream=%d err=%v", sess.ID, stream.StreamIdentifier(), err)
			}
			return
		}
		payload := buf[:n]
		switch uint32(ppid) {
		case PPIDDCEP:
			if n < 1 {
				continue
			}
			if payload[0] == DCEPMsgOpen {
				open, err := ParseDCEPOpen(payload)
				if err != nil {
					log.Printf("[sfu] dcep open parse session=%s err=%v", sess.ID, err)
					continue
				}
				label = open.Label
				st := sess.initSCTPState()
				st.mu.Lock()
				st.channels[label] = &dataChannel{stream: stream, label: label}
				st.mu.Unlock()
				dcChans.Add(1)
				log.Printf("[sfu] datachannel open session=%s label=%q proto=%q stream=%d",
					sess.ID, label, open.Protocol, stream.StreamIdentifier())
				ack := BuildDCEPAck()
				if _, err := stream.WriteSCTP(ack, sctp.PayloadProtocolIdentifier(PPIDDCEP)); err != nil {
					log.Printf("[sfu] dcep ack write err=%v", err)
				}
			}
		case PPIDStringUTF8, PPIDBinary, PPIDStringEmpty, PPIDBinaryEmpty:
			if label == "" {
				// Mensagem antes de OPEN — ignora.
				continue
			}
			dcMsgIn.Add(1)
			srv.forwardDC(sess, label, payload, uint32(ppid))
		}
	}
}

// forwardDC reenvia uma mensagem pra todos os outros peers, no stream
// com mesmo label. Abre o stream + DCEP_OPEN se ainda não existir.
func (srv *Server) forwardDC(from *Session, label string, data []byte, ppid uint32) {
	srv.router.mu.RLock()
	targets := make([]*Session, 0, len(srv.router.ses))
	for _, s := range srv.router.ses {
		if s != from {
			targets = append(targets, s)
		}
	}
	srv.router.mu.RUnlock()

	for _, sub := range targets {
		ch, err := srv.ensureDCStream(sub, label)
		if err != nil {
			continue
		}
		if _, err := ch.stream.WriteSCTP(data, sctp.PayloadProtocolIdentifier(ppid)); err == nil {
			dcMsgFwd.Add(1)
		}
	}
}

// ensureDCStream garante que `sub` tem um DataChannel com `label`
// aberto (cria + DCEP_OPEN se preciso).
func (srv *Server) ensureDCStream(sub *Session, label string) (*dataChannel, error) {
	st := sub.initSCTPState()
	st.mu.Lock()
	if ch, ok := st.channels[label]; ok {
		st.mu.Unlock()
		return ch, nil
	}
	assoc := st.assoc
	st.mu.Unlock()
	if assoc == nil {
		return nil, errors.New("sctp not ready")
	}
	// pion/sctp escolhe stream ID automaticamente; usamos o próximo livre.
	stream, err := assoc.OpenStream(nextStreamID(st), sctp.PayloadTypeWebRTCBinary)
	if err != nil {
		return nil, err
	}
	open := buildDCEPOpen(label)
	if _, err := stream.WriteSCTP(open, sctp.PayloadProtocolIdentifier(PPIDDCEP)); err != nil {
		_ = stream.Close()
		return nil, err
	}
	ch := &dataChannel{stream: stream, label: label}
	st.mu.Lock()
	st.channels[label] = ch
	st.mu.Unlock()
	dcChans.Add(1)
	return ch, nil
}

// nextStreamID gera um stream ID server-iniciado (ímpar por convenção
// quando o cliente é DTLS server; aqui o SFU é cliente SCTP então usamos
// pares e o browser usa ímpares). Suficiente pra demo; produção precisa
// negociar SID via DCEP corretamente.
func nextStreamID(st *sctpState) uint16 {
	st.mu.Lock()
	defer st.mu.Unlock()
	id := uint16(len(st.channels)*2 + 2) // 2,4,6,…
	return id
}

func buildDCEPOpen(label string) []byte {
	lb := []byte(label)
	out := make([]byte, 12+len(lb))
	out[0] = DCEPMsgOpen
	out[1] = 0 // reliable
	binary.BigEndian.PutUint16(out[2:4], 256)
	binary.BigEndian.PutUint32(out[4:8], 0)
	binary.BigEndian.PutUint16(out[8:10], uint16(len(lb)))
	binary.BigEndian.PutUint16(out[10:12], 0)
	copy(out[12:], lb)
	return out
}

// shutdownSCTP fecha a associação (best-effort).
func (s *Session) shutdownSCTP(_ context.Context) {
	s.mu.Lock()
	st := s.sctp
	s.mu.Unlock()
	if st == nil {
		return
	}
	st.mu.Lock()
	assoc := st.assoc
	st.mu.Unlock()
	if assoc != nil {
		_ = assoc.Close()
	}
}
