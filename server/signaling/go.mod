module github.com/webrtc-own/signaling

go 1.22

require (
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/webrtc-own/common v0.0.0
)

replace github.com/webrtc-own/common => ../common
