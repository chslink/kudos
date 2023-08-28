package rpcClientService

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/chslink/kudos/filter"
	"github.com/chslink/kudos/log"
	logs "github.com/chslink/kudos/log/beego"
	"github.com/chslink/kudos/rpcx/client"
	"github.com/chslink/kudos/rpcx/protocol"
)

var _rpcClientService *RpcClientService
var once sync.Once

type RpcClientService struct {
	opts *Options

	rpcClient *client.OneClient
	lock      sync.RWMutex
	rpcFilter filter.Filter
}

func GetRpcClientService() *RpcClientService {
	once.Do(func() {
		_rpcClientService = &RpcClientService{}
	})

	return _rpcClientService
}

func (r *RpcClientService) Initialize(opts ...Option) {
	options := newOptions(opts...)
	r.opts = options
	var d client.ServiceDiscovery
	switch r.opts.RegistryType {
	case "consul":
		d, _ = client.NewConsulDiscovery(r.opts.BasePath, "", []string{r.opts.RegistryAddr}, nil)
	case "etcd":
		d, _ = client.NewEtcdDiscovery(r.opts.BasePath, "", []string{r.opts.RegistryAddr}, nil)
	case "etcdv3":
		d, _ = client.NewEtcdV3Discovery(r.opts.BasePath, "", []string{r.opts.RegistryAddr}, nil)
	case "zookeeper":
		d, _ = client.NewZookeeperDiscovery(r.opts.BasePath, "", []string{r.opts.RegistryAddr}, nil)
	}

	var s client.SelectMode
	switch r.opts.SelectMode {
	case "RoundRobin":
		s = client.RoundRobin
	case "WeightedRoundRobin":
		s = client.WeightedRoundRobin
	case "WeightedICMP":
		s = client.WeightedICMP
	case "ConsistentHash":
		s = client.ConsistentHash
	case "Closest":
		s = client.Closest
	default:
		s = client.RandomSelect
	}

	r.lock.Lock()
	r.rpcClient = client.NewOneClient(client.Failtry, s, d, client.DefaultOption)
	r.lock.Unlock()
}

func (r *RpcClientService) Call(route string, session protocol.ISession, args interface{}, reply interface{}) error {
	rr := strings.Split(route, ".")
	if len(rr) < 3 {
		return errors.New("invalid route")
	}
	nodeName := rr[0]
	servicePath := rr[1]
	serviceMethod := rr[2]

	if r.rpcFilter != nil {
		r.rpcFilter.Before(servicePath+"."+serviceMethod, args)
	}
	r.lock.RLock()
	err := r.rpcClient.Call(context.TODO(), nodeName, servicePath, serviceMethod, session, args, reply)
	if err != nil {
		log.Error("rpc call error: %v", err)
	}
	r.lock.RUnlock()
	if r.rpcFilter != nil {
		r.rpcFilter.After(servicePath+"."+serviceMethod, reply)
	}
	return err
}

func (r *RpcClientService) Go(route string, session protocol.ISession, args interface{}, reply interface{}, chanRet chan *client.Call) {
	rr := strings.Split(route, ".")
	if len(rr) < 3 {
		logs.Error("invalid route")
		return
	}
	nodeName := rr[0]
	servicePath := rr[1]
	serviceMethod := rr[2]

	r.lock.RLock()
	defer r.lock.RUnlock()
	if _, err := r.rpcClient.Go(context.TODO(), nodeName, servicePath, serviceMethod, session, args, reply, chanRet); err != nil {
		log.Error("rpc go error: %v", err)
	}
}

// Set a filter for rpc
func (r *RpcClientService) SetRpcFilter(f filter.Filter) {
	r.rpcFilter = f
}
