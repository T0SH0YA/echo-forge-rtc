// Package common — tipos compartilhados entre signaling/stun/turn/sfu.
package common

// Envelope do protocolo de sinalização. Espelha docs/protocol/signaling.md.
type Envelope struct {
	T    string `json:"t"`
	ID   string `json:"id,omitempty"`
	Data any    `json:"data,omitempty"`
}

// Hello é o primeiro frame que o cliente manda.
type Hello struct {
	SDKVersion   string   `json:"sdkVersion"`
	Capabilities []string `json:"capabilities"`
}

// IceServer espelha RTCIceServer do browser.
type IceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// PeerInfo descreve um peer dentro de uma sala.
type PeerInfo struct {
	ID   string `json:"id"`
	Role string `json:"role,omitempty"`
}

// Welcome é a resposta do servidor ao Hello.
type Welcome struct {
	PeerID     string      `json:"peerId"`
	Room       string      `json:"room"`
	Peers      []PeerInfo  `json:"peers"`
	IceServers []IceServer `json:"iceServers"`
}

// SDPMessage é offer ou answer.
type SDPMessage struct {
	To   string `json:"to,omitempty"`
	From string `json:"from,omitempty"`
	SDP  string `json:"sdp"`
}

// IceMessage é um candidate trickle.
type IceMessage struct {
	To            string `json:"to,omitempty"`
	From          string `json:"from,omitempty"`
	Candidate     string `json:"candidate"`
	SDPMid        string `json:"sdpMid,omitempty"`
	SDPMLineIndex *int   `json:"sdpMLineIndex,omitempty"`
}

// ErrorMessage padroniza erros do servidor.
type ErrorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
