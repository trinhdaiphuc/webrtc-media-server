package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v3"

	"github.com/trinhdaiphuc/webrtc-media-server/internal/model"
	"github.com/trinhdaiphuc/webrtc-media-server/pkg/log"
)

type peerConnectionState struct {
	peerConnection *webrtc.PeerConnection
	websocket      net.Conn
}

var (
	ll              = log.New()
	listLock        sync.RWMutex
	peerConnections []peerConnectionState
	trackLocals     = make(map[string]*webrtc.TrackLocalStaticRTP)
)

// signalPeerConnections updates each PeerConnection so that it is getting all the expected media tracks
func signalPeerConnections() {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		DispatchKeyFrame()
	}()

	attemptSync := func() (tryAgain bool) {
		for i := range peerConnections {
			if peerConnections[i].peerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
				peerConnections = append(peerConnections[:i], peerConnections[i+1:]...)
				return true // We modified the slice, start from the beginning
			}

			// map of sender we already are sending, so we don't double send
			existingSenders := map[string]bool{}

			for _, sender := range peerConnections[i].peerConnection.GetSenders() {
				if sender.Track() == nil {
					continue
				}

				existingSenders[sender.Track().ID()] = true

				// If we have a RTPSender that doesn't map to an existing track remove and signal
				if _, ok := trackLocals[sender.Track().ID()]; !ok {
					if err := peerConnections[i].peerConnection.RemoveTrack(sender); err != nil {
						return true
					}
				}
			}

			// Don't receive videos we are sending, make sure we don't have loopback
			for _, receiver := range peerConnections[i].peerConnection.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			// Add all track we aren't sending yet to the PeerConnection
			for trackID := range trackLocals {
				if _, ok := existingSenders[trackID]; !ok {
					if _, err := peerConnections[i].peerConnection.AddTrack(trackLocals[trackID]); err != nil {
						return true
					}
				}
			}

			offer, err := peerConnections[i].peerConnection.CreateOffer(nil)
			if err != nil {
				ll.Error("Create offer error", log.Error(err))
				return true
			}

			if err = peerConnections[i].peerConnection.SetLocalDescription(offer); err != nil {
				ll.Error("Set local description", log.Error(err))
				return true
			}

			msg, err := model.BuildSDPOfferMessage(offer)
			if err != nil {
				ll.Error("build SDP offer", log.Error(err))
				return true
			}
			err = wsutil.WriteServerText(peerConnections[i].websocket, msg.ToByte())
			if err != nil {
				ll.Error("Write websocket message error", log.Error(err))
				return true
			}
		}

		return
	}

	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			// Release the lock and attempt a sync in 3 seconds. We might be blocking a RemoveTrack or AddTrack
			go func() {
				time.Sleep(time.Second * 3)
				signalPeerConnections()
			}()
			return
		}

		if !attemptSync() {
			break
		}
	}
}

func WSHandler(w http.ResponseWriter, r *http.Request) {
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		ll.Error("Upgrade error", log.Error(err))
		return
	}
	defer conn.Close()

	peerConnectionConfig := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create new PeerConnection
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		ll.Error("Register codec", log.Error(err))
		return
	}

	// Setting webrtc engine
	se := webrtc.SettingEngine{}
	se.DisableMediaEngineCopy(true)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6})
	err = se.SetEphemeralUDPPortRange(50000, 60000)
	if err != nil {
		ll.Error("Set ephemeral UDP port range", log.Error(err))
		return
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithSettingEngine(se))
	peerConnection, err := api.NewPeerConnection(peerConnectionConfig)
	if err != nil {
		ll.Error("New peer connection", log.Error(err))
		return
	}

	// When this frame returns close the PeerConnection
	defer peerConnection.Close() //nolint

	// Accept one audio and one video track incoming
	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			ll.Error("Add transceiver from kind", log.Error(err))
			return
		}
	}

	// Add our new PeerConnection to global list
	listLock.Lock()
	peerConnections = append(peerConnections, peerConnectionState{peerConnection, conn})
	listLock.Unlock()

	// Trickle ICE. Emit server candidate to client
	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		msg, err := model.BuildICECandidateMessage(i)
		if err != nil {
			ll.Error("Build ICE candidate message failed", log.Error(err))
			return
		}

		if err := wsutil.WriteServerText(conn, msg.ToByte()); err != nil {
			ll.Error("Write websocket message error", log.Error(err))
		}
	})

	// If PeerConnection is closed remove it from global list
	peerConnection.OnConnectionStateChange(func(p webrtc.PeerConnectionState) {
		switch p {
		case webrtc.PeerConnectionStateFailed:
			if err := peerConnection.Close(); err != nil {
				ll.Error("Close peer connection", log.Error(err))
			}
		case webrtc.PeerConnectionStateClosed:
			signalPeerConnections()
		}
	})

	// Set a handler for when a new remote track starts, this just distributes all our packets
	// to connected peers
	peerConnection.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// Create a track to fan out our incoming video to all peers
		localTrack, err := addTrack(remoteTrack)
		if err != nil {
			ll.Error("Add remote track error", log.Error(err))
			return
		}
		defer removeTrack(localTrack)

		rtpBuf := make([]byte, 1400)
		for {
			i, _, readErr := remoteTrack.Read(rtpBuf)
			if readErr != nil {
				ll.Error("Read buffer error", log.Error(readErr))
				return
			}

			// ErrClosedPipe means we don't have any subscribers, this is ok if no peers have connected yet
			if _, err = localTrack.Write(rtpBuf[:i]); err != nil && !errors.Is(err, io.ErrClosedPipe) {
				ll.Error("Write buffer error", log.Error(err))
				return
			}
		}
	})

	// Signal for the new PeerConnection
	signalPeerConnections()

	message := &model.WebsocketMessage{}
	for {
		msg, _, err := wsutil.ReadClientData(conn)
		if err != nil {
			ll.Error("Read websocket message from client error", log.Error(err))
			return
		}
		err = json.Unmarshal(msg, message)
		if err != nil {
			ll.Error("Unmarshal message error", log.Error(err))
			return
		}

		switch message.Event {
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				ll.Error("Unmarshal message error", log.Error(err))
				return
			}
			if err := peerConnection.AddICECandidate(candidate); err != nil {
				ll.Error("Add ICE candidate error", log.Error(err))
				return
			}
		case "answer":
			answer := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &answer); err != nil {
				ll.Error("Unmarshal SDP answer error", log.Error(err))
				return
			}

			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				ll.Error("Set remote description error", log.Error(err))
				return
			}
		}
	}
}

// DispatchKeyFrame sends a keyframe to all PeerConnections, used everytime a new user joins the call
// Send a PLI on an interval so that the publisher is pushing a keyframe every rtcpPLIInterval
// This can be less wasteful by processing incoming RTCP events, then we would emit a NACK/PLI when a viewer requests it
func DispatchKeyFrame() {
	listLock.Lock()
	defer listLock.Unlock()

	for i := range peerConnections {
		for _, receiver := range peerConnections[i].peerConnection.GetReceivers() {
			if receiver.Track() == nil {
				continue
			}

			_ = peerConnections[i].peerConnection.WriteRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{
					MediaSSRC: uint32(receiver.Track().SSRC()),
				},
			})
		}
	}
}

// Add to list of tracks and fire renegotiation for all PeerConnections
func addTrack(t *webrtc.TrackRemote) (*webrtc.TrackLocalStaticRTP, error) {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections()
	}()

	// Create a new TrackLocal with the same codec as our incoming
	trackLocal, err := webrtc.NewTrackLocalStaticRTP(t.Codec().RTPCodecCapability, t.ID(), t.StreamID())
	if err != nil {
		return nil, err
	}

	trackLocals[t.ID()] = trackLocal
	return trackLocal, nil
}

// Remove from list of tracks and fire renegotiation for all PeerConnections
func removeTrack(t *webrtc.TrackLocalStaticRTP) {
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		signalPeerConnections()
	}()

	delete(trackLocals, t.ID())
}
