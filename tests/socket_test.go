// Copyright 2018 The Nakama Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tests

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/gorilla/websocket"
	"github.com/heroiclabs/nakama/rtapi"
	"github.com/satori/go.uuid"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

const maxConn = 2000

type protectedWebSocketConn struct {
	id int
	c  *websocket.Conn
	mu sync.Mutex
}

func (p *protectedWebSocketConn) send(t *testing.T, v proto.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()

	j, _ := jsonpbMarshaler.MarshalToString(v)
	if err := p.c.WriteMessage(websocket.TextMessage, []byte(j)); err != nil {
		t.Fatal("write:", err)
	}
}

func TestMaxSocketOpen_MatchExchange(t *testing.T) {
	done := make(chan bool)
	sessions := make([]string, 0)
	wsConns := make([]*protectedWebSocketConn, 0)
	matchId := ""

	for i := 0; i <= maxConn; i++ {
		conn, _, session, _ := NewSession(t, uuid.Must(uuid.NewV4()).String())
		conn.Close()
		sessions = append(sessions, session.Token)
	}

	joinMatches := func() {
		go func() {
			for _, c := range wsConns[1:] {
				matchJoinMessage := &rtapi.Envelope{
					Message: &rtapi.Envelope_MatchJoin{
						MatchJoin: &rtapi.MatchJoin{
							Id: &rtapi.MatchJoin_MatchId{
								MatchId: matchId,
							},
						},
					},
				}
				logger.Info(fmt.Sprintf("Session %d - Joining match...", c.id))
				c.send(t, matchJoinMessage)
			}
		}()
	}

	sendMatchData := func() {
		for {
			go func() {
				i := rand.Intn(len(wsConns))
				c := wsConns[i]

				matchDataSendMessage := &rtapi.Envelope{
					Message: &rtapi.Envelope_MatchDataSend{
						MatchDataSend: &rtapi.MatchDataSend{
							MatchId: matchId,
							OpCode:  10,
							Data:    []byte("TestMaxSocketOpen"),
						},
					},
				}

				logger.Info(fmt.Sprintf("Session %d - Sending match data...", c.id))
				c.send(t, matchDataSendMessage)
			}()
		}
	}

	logger.Info(fmt.Sprintf("Session %d - Connecting...", 0))
	firstPC := &protectedWebSocketConn{id: 0}
	firstConn := NewWebSocketConnection(t, sessions[0], func(envelope *rtapi.Envelope) {
		if envelope.GetMatch() != nil {
			matchId = envelope.GetMatch().GetMatchId()
			logger.Info(fmt.Sprintf("Session %d - Created match.", firstPC.id), zap.String("match_id", matchId))
			joinMatches()
		}
	})
	firstPC.c = firstConn
	wsConns = append(wsConns, firstPC)
	logger.Info(fmt.Sprintf("Session %d - Connected.", firstPC.id))

	countJoinedMatch := atomic.NewInt64(0)
	for i, s := range sessions[1:] {
		pc := &protectedWebSocketConn{id: i}
		logger.Info(fmt.Sprintf("Session %d - Connecting...", pc.id))
		port := 7350
		if i > len(sessions)/3 {
			port = 7370
		}

		c := NewWebSocketConnectionPort(t, port, s, func(envelope *rtapi.Envelope) {
			if envelope.GetMatch() != nil {
				logger.Info(fmt.Sprintf("Session %d - Joined match.", pc.id))
				countJoinedMatch.Inc()
				if countJoinedMatch.Load() == int64(len(wsConns)-1) {
					sendMatchData()
				}
			}
		})
		pc.c = c
		logger.Info(fmt.Sprintf("Session %d - Connected.", pc.id))
		defer c.Close()
		wsConns = append(wsConns, pc)
	}

	matchCreateMessage := &rtapi.Envelope{
		Message: &rtapi.Envelope_MatchCreate{
			MatchCreate: &rtapi.MatchCreate{},
		},
	}
	logger.Info(fmt.Sprintf("Session %d - Creating match...", firstPC.id))
	firstPC.send(t, matchCreateMessage)

	select {
	case <-done:
	case <-time.After(50 * time.Second):
		t.Fatal("timeout")
	}
}
