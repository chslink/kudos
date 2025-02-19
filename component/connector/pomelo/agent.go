package pomelo

import (
	"net"
	"reflect"

	"github.com/chslink/kudos/log"
	"github.com/chslink/kudos/network"
	"github.com/chslink/kudos/protocol"
	"github.com/chslink/kudos/protocol/pomelo/message"
	"github.com/chslink/kudos/protocol/pomelo/pkg"
	"github.com/chslink/kudos/rpc"
	"github.com/chslink/kudos/rpcx/client"
	"github.com/chslink/kudos/service/codecService"
)

type agent struct {
	conn         network.Conn
	connector    *Connector
	session      *rpc.Session
	userData     interface{}
	agentHandler *agentHandler
	chanRet      chan *client.Call
	writeChan    chan *[]byte
}

func NewAgent(conn network.Conn, connector *Connector) *agent {
	a := &agent{
		conn:      conn,
		connector: connector,
		session:   rpc.NewSession(connector.nodeId),
		userData:  nil,
		chanRet:   make(chan *client.Call, 100),
		writeChan: make(chan *[]byte, 100),
	}
	a.agentHandler = NewAgentHandler(a)
	return a
}

func (a *agent) Run() {
	go func() {
		defer a.conn.Close()
		for {
			select {
			case ri := <-a.chanRet:
				if ri.Error != nil {
					log.Error("failed to call: %v", ri.Error)
				} else {
					args := ri.Args.(*rpc.Args)
					if a.connector.handlerFilter != nil {
						a.connector.handlerFilter.After(ri.ServicePath+"."+ri.ServiceMethod, ri)
					}

					a.WriteResponse(args.MsgId, ri.Reply)
				}
			case b := <-a.writeChan:
				if b == nil {
					return
				}

				err := a.conn.WriteMessage(*b)
				protocol.FreePoolBuffer(b)
				if err != nil {
					log.Error("ws WriteMessage: %s", err.Error())
					return
				}
			}
		}
	}()

	for {
		buffer := protocol.GetPoolMsg()
		err := a.conn.ReadMsg(buffer)
		if err != nil {
			log.Debug("read message: %v", err)
			break
		}

		a.agentHandler.Handle(buffer)
		buffer.Reset()
		protocol.FreePoolMsg(buffer)
	}
	close(a.writeChan)
}

func (a *agent) OnClose() {
	if a.agentHandler.timerHandler != nil {
		a.connector.timers.ClearTimeout(a.agentHandler.timerHandler)
	}
	a.connector.connection.OnDisconnect(a.session)
	a.connector.sessions.DelSession(a)
}

func (a *agent) WriteResponse(msgId int, msg interface{}) {
	_codec := codecService.GetCodecService()
	if _codec != nil {
		data, err := _codec.Marshal(msg)
		if err != nil {
			log.Error("marshal message %v error: %v", reflect.TypeOf(msg), err)
			return
		}
		//routeId := msgService.GetMsgService().GetRouteId(route)
		buffer := message.Encode(msgId, message.TYPE_RESPONSE, 0, data)
		err = a.conn.WriteMessage(*pkg.Encode(pkg.TYPE_DATA, buffer))
		protocol.FreePoolBuffer(&buffer)
		if err != nil {
			log.Error("write message %v error: %v", reflect.TypeOf(msg), err)
		}
	}
}

// Write to channel. Make sure buffer from protocol.GetPoolBuffer()
func (a *agent) Write(data *[]byte) {
	a.writeChan <- data
}

func (a *agent) LocalAddr() net.Addr {
	return a.conn.LocalAddr()
}

func (a *agent) RemoteAddr() net.Addr {
	return a.conn.RemoteAddr()
}

func (a *agent) Close() {
	a.conn.Close()
}

func (a *agent) UserData() interface{} {
	return a.userData
}

func (a *agent) SetUserData(data interface{}) {
	a.userData = data
}

func (a *agent) GetSession() *rpc.Session {
	return a.session
}

func (a *agent) PushMessage(routeId uint32, data []byte) {
	buffer := message.Encode(0, message.TYPE_PUSH, uint16(routeId), data)
	a.Write(pkg.Encode(pkg.TYPE_DATA, buffer))
}

func (a *agent) KickMessage(reason string) {
	ret := map[string]string{
		"reason": reason,
	}
	buffer, _ := codecService.GetCodecService().Marshal(ret)
	a.Write(pkg.Encode(pkg.TYPE_KICK, buffer))
}
