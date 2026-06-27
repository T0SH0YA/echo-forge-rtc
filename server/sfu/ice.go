// Agente ICE-lite: o servidor não inicia checks, apenas responde Binding
// requests do peer. Quando o peer marca um par com USE-CANDIDATE, fixamos
// aquele endereço como remoto da sessão (RFC 8445 §7.3 simplificado pra
// ICE-lite, §6.1.3).
package main

import (
	"log"
	"net"
)

// HandleBinding processa um Binding Request UDP. Retorna (resposta, sessão,
// destino) ou (nil, nil, nil) se rejeitar silenciosamente.
func (s *Server) HandleBinding(raw []byte, from *net.UDPAddr) ([]byte, *Session) {
	msg, err := Decode(raw)
	if err != nil {
		return nil, nil
	}
	if methodOf(msg.Type) != methodBinding || classOf(msg.Type) != classRequest {
		return nil, nil
	}
	userV, ok := msg.Get(AttrUsername)
	if !ok {
		return nil, nil
	}
	localUfrag, _, ok := splitUsername(string(userV))
	if !ok {
		return nil, nil
	}
	sess := s.sessions.ByLocalUfrag(localUfrag)
	if sess == nil {
		// Sessão ainda não negociada — descarta.
		return nil, nil
	}
	// MESSAGE-INTEGRITY usa nossa LocalPwd (short-term credential, RFC 5389 §15.4).
	if !VerifyMessageIntegrity(raw, []byte(sess.LocalPwd)) {
		log.Printf("[sfu] ice binding MI fail from=%s ufrag=%s", from, localUfrag)
		resp := &Message{Type: msgType(methodBinding, classError), TransactionID: msg.TransactionID}
		resp.Add(AttrErrorCode, EncodeErrorCode(401, "Unauthorized"))
		return AppendFingerprint(resp.Encode()), sess
	}
	sess.markChecking()

	useCand := msg.Has(AttrUseCandidate)

	resp := &Message{Type: msgType(methodBinding, classSuccess), TransactionID: msg.TransactionID}
	resp.Add(AttrXORMappedAddress, EncodeXORAddr(from, msg.TransactionID))
	resp.Add(AttrSoftware, []byte(softwareName))
	// Resposta deve carregar MI com a MESMA short-term key (LocalPwd, conforme
	// RFC 5245 §7.1.2.4) + FINGERPRINT.
	out := AppendMessageIntegrity(resp.Encode(), []byte(sess.LocalPwd))
	out = AppendFingerprint(out)

	if useCand {
		sess.markConnected(from.String())
		log.Printf("[sfu] ice connected session=%s peer=%s", sess.ID, from)
	}
	return out, sess
}
