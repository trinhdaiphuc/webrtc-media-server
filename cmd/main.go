package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"

	"github.com/trinhdaiphuc/webrtc-media-server/web"
)

type websocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

func (w *websocketMessage) ToByte() []byte {
	data, err := json.Marshal(w)
	if err != nil {
		return nil
	}
	return data
}

const (
	rtcpPLIInterval = time.Second * 3
)

var (
	addr = flag.String("addr", ":8080", "http service address")
)

func init() {
	flag.Parse()
}

func main() {
	mux := http.NewServeMux()
	mux.Handle("/", web.Handler())
	mux.HandleFunc("/websocket", wsHandler)

	// start HTTP server
	log.Printf("starting http server on http://localhost%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Printf("upgrade error: %s", err)
		return
	}
	defer conn.Close()

	// Create new PeerConnection
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		log.Print(err)
		return
	}

	// When this frame returns close the PeerConnection
	defer peerConnection.Close() //nolint

	// Accept one audio and one video track incoming
	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			log.Print(err)
			return
		}
	}

	// Trickle ICE. Emit server candidate to client
	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}

		candidateString, err := json.Marshal(i.ToJSON())
		if err != nil {
			log.Println(err)
			return
		}

		log.Printf("Candidate %s", candidateString)

		msg := &websocketMessage{
			Event: "candidate",
			Data:  string(candidateString),
		}
		if err := wsutil.WriteServerText(conn, msg.ToByte()); err != nil {
			log.Println(err)
		}
	})

	// Set a handler for when a new remote track starts, this just distributes all our packets
	// to connected peers
	peerConnection.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
		// This can be less wasteful by processing incoming RTCP events, then we would emit a NACK/PLI when a viewer requests it
		go func() {
			ticker := time.NewTicker(rtcpPLIInterval)
			for range ticker.C {
				if rtcpSendErr := peerConnection.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(remoteTrack.SSRC())}}); rtcpSendErr != nil {
					fmt.Println(rtcpSendErr)
				}
			}
		}()

		// Create a local track, all our SFU clients will be fed via this track
		localTrack, newTrackErr := webrtc.NewTrackLocalStaticRTP(remoteTrack.Codec().RTPCodecCapability, "video", "pion")
		if newTrackErr != nil {
			panic(newTrackErr)
		}

		rtpBuf := make([]byte, 1400)
		for {
			i, _, readErr := remoteTrack.Read(rtpBuf)
			if readErr != nil {
				panic(readErr)
			}

			// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet
			if _, err = localTrack.Write(rtpBuf[:i]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				panic(err)
			}
		}
	})

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		log.Println(err)
		return
	}
	offerString, err := json.Marshal(offer)
	if err != nil {
		return
	}
	msg := &websocketMessage{
		Event: "offer",
		Data:  string(offerString),
	}
	err = wsutil.WriteServerText(conn, msg.ToByte())
	if err != nil {
		log.Println(err)
		return
	}

	message := &websocketMessage{}
	for {
		msg, _, err := wsutil.ReadClientData(conn)
		if err != nil {
			log.Println(err)
			return
		}
		err = json.Unmarshal(msg, message)
		if err != nil {
			log.Println(err)
			return
		}

		switch message.Event {
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Println(err)
				return
			}

			if err := peerConnection.AddICECandidate(candidate); err != nil {
				log.Println(err)
				return
			}
		case "answer":
			answer := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &answer); err != nil {
				log.Println(err)
				return
			}

			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				log.Println(err)
				return
			}
		}
	}
}
