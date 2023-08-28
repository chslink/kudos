package protobuf

import (
	"reflect"
	"strings"

	"github.com/chslink/kudos/log"
	"github.com/chslink/kudos/protocol/protobuf/pkg"
	"github.com/chslink/kudos/rpc"
	"github.com/chslink/kudos/service/msgService"
	"github.com/chslink/kudos/utils/timer"
)

type agentHandler struct {
	agent        *agent
	timerHandler *timer.Timer
}

func NewAgentHandler(a *agent) *agentHandler {
	return &agentHandler{agent: a}
}

func (h *agentHandler) Handle(pkgType uint32, body []byte) {
	switch pkgType {
	case uint32(pkg.EMsgType_TYPE_HEARTBEAT):
		h.handleHeartbeat(pkgType, body)
	default:
		h.handleData(pkgType, body)
	}
}

func (h *agentHandler) handleHeartbeat(pkgType uint32, body []byte) {
	buffer := pkg.Encode(uint32(pkg.EMsgType_TYPE_HEARTBEAT), nil)
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

func (h *agentHandler) handleData(pkgType uint32, body []byte) {
	msgId, data := pkgType, body

	msgInfo := msgService.GetMsgService().GetMsgByRouteId(msgId)
	if msgInfo == nil {
		log.Error("invalid route id")
		return
	}

	args := &rpc.Args{
		MsgId:  int(msgInfo.RespId),
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
			reply := &pkg.RespResult{
				Code: int32(pkg.EErrorCode_ERROR_ROUTE_ID),
				Msg:  err.Error(),
			}
			h.agent.WriteResponse(int(pkg.EMsgType_TYPE_COMMON_RESULT), reply)
			return
		}
	}
	if h.agent.connector.handlerFilter != nil {
		h.agent.connector.handlerFilter.Before(servicePath+"."+serviceName, args)
	}
	h.agent.connector.proxy.Go(nodeName, servicePath, serviceName, h.agent.session, args, msgResp, h.agent.chanRet)
	//rpcClientService.GetRpcClientService().Go(nodeName, servicePath, serviceName, h.agent.session, args, msgResp, h.agent.chanRet)
}
