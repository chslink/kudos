package pomelo

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/chslink/kudos/log"
	"github.com/chslink/kudos/protocol/pomelo/message"
	"github.com/chslink/kudos/protocol/pomelo/pkg"
	"github.com/chslink/kudos/rpc"
	"github.com/chslink/kudos/service/msgService"
	"github.com/chslink/kudos/utils/timer"
)

const (
	CODE_OK         = 200
	CODE_USE_ERROR  = 500
	CODE_OLD_CLIENT = 501
)

type agentHandler struct {
	agent        *agent
	timerHandler *timer.Timer
}

func NewAgentHandler(a *agent) *agentHandler {
	return &agentHandler{agent: a}
}

func (h *agentHandler) Handle(buffer *bytes.Buffer) {
	pkgType, body := pkg.Decode(buffer.Bytes())
	switch pkgType {
	case pkg.TYPE_HANDSHAKE:
		h.handleHandshake(pkgType, body)
	case pkg.TYPE_HANDSHAKE_ACK:
		h.handleHandshakeAck(pkgType, body)
	case pkg.TYPE_HEARTBEAT:
		h.handleHeartbeat(pkgType, body)
	case pkg.TYPE_DATA:
		h.handleData(pkgType, body)
	}
}

func (h *agentHandler) handleHandshake(pkgType int, body []byte) {
	var message map[string]json.RawMessage
	err := json.Unmarshal(body, &message)
	if err != nil {
		log.Error("handshake decode error: %v", err)
		h.processError(CODE_USE_ERROR)
		return
	}

	if message["sys"] == nil {
		h.processError(CODE_USE_ERROR)
		return
	}

	sys := make(map[string]interface{})
	res := make(map[string]interface{})
	res["code"] = CODE_OK
	res["sys"] = sys
	sys["heartbeat"] = h.agent.connector.opts.HeartbeatTimeout / time.Second
	sys["useDict"] = true
	sys["dict"] = msgService.GetMsgService().GetMsgMap()

	bin, _ := json.Marshal(res)
	buffer := pkg.Encode(pkg.TYPE_HANDSHAKE, bin)
	h.agent.Write(buffer)
}

func (h *agentHandler) handleHandshakeAck(pkgType int, body []byte) {
	h.handleHeartbeat(pkgType, body)
}

func (h *agentHandler) handleHeartbeat(pkgType int, body []byte) {
	buffer := pkg.Encode(pkg.TYPE_HEARTBEAT, nil)
	h.agent.Write(buffer)

	if h.timerHandler != nil {
		h.agent.connector.timers.ClearTimeout(h.timerHandler)
	}

	//heartbeat timeout close the socket
	h.timerHandler = h.agent.connector.timers.AfterFunc(2*h.agent.connector.opts.HeartbeatTimeout, func() {
		log.Debug("heart beat overtime")
		h.agent.Close()
	})
}

func (h *agentHandler) handleData(pkgType int, body []byte) {
	msgId, msgType, route, data := message.Decode(body)
	//_ = msgId
	_ = msgType

	msgInfo := msgService.GetMsgService().GetMsgByRouteId(uint32(route))
	if msgInfo == nil {
		log.Error("invalid route id")
		return
	}

	args := &rpc.Args{
		MsgId:  msgId,
		MsgReq: data,
	}

	msgResp := reflect.New(msgInfo.MsgRespType.Elem()).Interface()
	rr := strings.Split(msgInfo.Route, ".")
	if len(rr) < 3 {
		log.Error("route format error")
		return
	}
	nodeName := rr[0]
	servicePath := rr[1]
	serviceName := rr[2]

	if h.agent.connector.customerRoute != nil {
		var err error
		servicePath, err = h.agent.connector.customerRoute(h.agent.session, servicePath, serviceName)
		if err != nil {
			log.Error("customer route error: %v", err)
			reply := &rpc.Reply{
				Code:   CODE_USE_ERROR,
				ErrMsg: err.Error(),
			}
			h.agent.WriteResponse(msgId, reply)
			return
		}
	}
	if h.agent.connector.handlerFilter != nil {
		h.agent.connector.handlerFilter.Before(servicePath+"."+serviceName, args)
	}
	h.agent.connector.proxy.Go(nodeName, servicePath, serviceName, h.agent.session, args, msgResp, h.agent.chanRet)
	//rpcClientService.GetRpcClientService().Go(nodeName, servicePath, serviceName, h.agent.session, args, msgResp, h.agent.chanRet)
}

func (h *agentHandler) processError(code int) {
	r := make(map[string]int)
	r["code"] = code
	bin, _ := json.Marshal(r)
	buffer := pkg.Encode(pkg.TYPE_HANDSHAKE, bin)
	h.agent.Write(buffer)
}
