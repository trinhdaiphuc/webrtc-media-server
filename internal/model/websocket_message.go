package model

import (
	"encoding/json"

	"github.com/pion/webrtc/v3"

	"github.com/trinhdaiphuc/webrtc-media-server/pkg/log"
)

type WebsocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

var (
	ll = log.New()
)

func (w *WebsocketMessage) ToByte() []byte {
	data, err := json.Marshal(w)
	if err != nil {
		return nil
	}
	return data
}

func BuildSDPOfferMessage(offer webrtc.SessionDescription) (*WebsocketMessage, error) {
	offerString, err := json.Marshal(offer)
	if err != nil {
		return nil, err
	}
	msg := &WebsocketMessage{
		Event: "offer",
		Data:  string(offerString),
	}
	return msg, nil
}

func BuildICECandidateMessage(ice *webrtc.ICECandidate) (*WebsocketMessage, error) {
	candidateString, err := json.Marshal(ice.ToJSON())
	if err != nil {
		ll.Error("Marshal candidate string", log.Error(err))
		return nil, err
	}

	msg := &WebsocketMessage{
		Event: "candidate",
		Data:  string(candidateString),
	}
	return msg, nil
}
