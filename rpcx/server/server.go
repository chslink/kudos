package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/chslink/kudos/rpcx/log"
	"github.com/chslink/kudos/rpcx/protocol"
	"github.com/chslink/kudos/rpcx/share"
)

// ErrServerClosed is returned by the Server's Serve, ListenAndServe after a call to Shutdown or Close.
var ErrServerClosed = errors.New("http: Server closed")

const (
	// ReaderBuffsize is used for bufio reader.
	ReaderBuffsize = 1024
	// WriterBuffsize is used for bufio writer.
	WriterBuffsize = 1024
)

// contextKey is a value for use with context.WithValue. It's used as
// a pointer so it fits in an interface{} without allocation.
type contextKey struct {
	name string
}

func (k *contextKey) String() string { return "rpcx context value " + k.name }

var (
	// RemoteConnContextKey is a context key. It can be used in
	// services with context.WithValue to access the connection arrived on.
	// The associated value will be of type net.Conn.
	RemoteConnContextKey = &contextKey{"remote-conn"}
	// StartRequestContextKey records the start time
	StartRequestContextKey = &contextKey{"start-parse-request"}
	// StartSendRequestContextKey records the start time
	StartSendRequestContextKey = &contextKey{"start-send-request"}
	// TagContextKey is used to record extra info in handling services. Its value is a map[string]interface{}
	TagContextKey = &contextKey{"service-tag"}
	// HttpConnContextKey is used to store http connection.
	HttpConnContextKey = &contextKey{"http-conn"}
)

// Server is rpcx server that use TCP or UDP.
type Server struct {
	ln                 net.Listener
	readTimeout        time.Duration
	writeTimeout       time.Duration
	gatewayHTTPServer  *http.Server
	DisableHTTPGateway bool // should disable http invoke or not.
	DisableJSONRPC     bool // should disable json rpc or not.

	serviceMapMu sync.RWMutex
	serviceMap   map[string]*Service

	mu         sync.RWMutex
	activeConn map[net.Conn]*ConnAgent
	doneChan   chan struct{}
	seq        uint64

	inShutdown int32
	onShutdown []func(s *Server)
	onRestart  []func(s *Server)

	// TLSConfig for creating tls tcp connection.
	tlsConfig *tls.Config
	// BlockCrypt for kcp.BlockCrypt
	options map[string]interface{}

	// CORS options
	corsOptions *CORSOptions

	Plugins PluginContainer

	// AuthFunc can be used to auth.
	AuthFunc func(ctx context.Context, req *protocol.Message, token string) error

	handlerMsgNum int32
}

// NewServer returns a server.
func NewServer(options ...OptionFn) *Server {
	s := &Server{
		Plugins:    &pluginContainer{},
		options:    make(map[string]interface{}),
		activeConn: make(map[net.Conn]*ConnAgent),
		doneChan:   make(chan struct{}),
		serviceMap: make(map[string]*Service),
	}

	for _, op := range options {
		op(s)
	}

	if s.options["TCPKeepAlivePeriod"] == nil {
		s.options["TCPKeepAlivePeriod"] = 3 * time.Minute
	}
	return s
}

// Address returns listened address.
func (s *Server) Address() net.Addr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// ActiveClientConn returns active connections.
func (s *Server) ActiveClientConn() []net.Conn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]net.Conn, 0, len(s.activeConn))
	for clientConn := range s.activeConn {
		result = append(result, clientConn)
	}
	return result
}

// SendMessage a request to the specified client.
// The client is designated by the conn.
// conn can be gotten from context in services:
//
//	ctx.Value(RemoteConnContextKey)
//
// servicePath, serviceMethod, metadata can be set to zero values.
func (s *Server) SendMessage(conn net.Conn, servicePath, serviceMethod string, metadata map[string]string, data []byte) error {
	ctx := share.WithValue(context.Background(), StartSendRequestContextKey, time.Now().UnixNano())
	s.Plugins.DoPreWriteRequest(ctx)

	req := protocol.GetPooledMsg()
	req.SetMessageType(protocol.Request)

	seq := atomic.AddUint64(&s.seq, 1)
	req.SetSeq(seq)
	req.SetOneway(true)
	req.SetSerializeType(protocol.SerializeNone)
	req.ServicePath = servicePath
	req.ServiceMethod = serviceMethod
	req.Metadata = metadata
	req.Payload = data

	b := req.EncodeSlicePointer()
	_, err := conn.Write(*b)
	protocol.PutData(b)

	s.Plugins.DoPostWriteRequest(ctx, req, err)
	protocol.FreeMsg(req)
	return err
}

func (s *Server) getDoneChan() <-chan struct{} {
	return s.doneChan
}

// startShutdownListener start a new goroutine to notify SIGTERM
// and SIGHUP signals and handle them gracefully
func (s *Server) startShutdownListener() {
	go func(s *Server) {
		log.Info("server pid:", os.Getpid())

		// channel to receive notifications of SIGTERM and SIGHUP
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)

		// custom functions to handle signal SIGTERM and SIGHUP
		var customFuncs []func(s *Server)

		switch <-ch {
		case syscall.SIGTERM:
			customFuncs = append(s.onShutdown, func(s *Server) {
				s.Shutdown(context.Background())
			})
		case syscall.SIGHUP:
			customFuncs = append(s.onRestart, func(s *Server) {
				s.Restart(context.Background())
			})
		}

		for _, fn := range customFuncs {
			fn(s)
		}
	}(s)
}

// Serve starts and listens RPC requests.
// It is blocked until receiving connections from clients.
func (s *Server) Serve(network, address string) (err error) {
	s.startShutdownListener()
	var ln net.Listener
	ln, err = s.makeListener(network, address)
	if err != nil {
		return
	}

	if network == "http" {
		s.serveByHTTP(ln, "")
		return nil
	}

	// try to start gateway
	ln = s.startGateway(network, ln)

	return s.serveListener(ln)
}

// ServeListener listens RPC requests.
// It is blocked until receiving connections from clients.
func (s *Server) ServeListener(network string, ln net.Listener) (err error) {
	s.startShutdownListener()
	if network == "http" {
		s.serveByHTTP(ln, "")
		return nil
	}

	// try to start gateway
	ln = s.startGateway(network, ln)

	return s.serveListener(ln)
}

// serveListener accepts incoming connections on the Listener ln,
// creating a new service goroutine for each.
// The service goroutines read requests and then call services to reply to them.
func (s *Server) serveListener(ln net.Listener) error {
	var tempDelay time.Duration

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	for {
		conn, e := ln.Accept()
		if e != nil {
			select {
			case <-s.getDoneChan():
				return ErrServerClosed
			default:
			}

			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}

				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}

				log.Errorf("rpcx: Accept error: %v; retrying in %v", e, tempDelay)
				time.Sleep(tempDelay)
				continue
			}

			if strings.Contains(e.Error(), "listener closed") {
				return ErrServerClosed
			}
			return e
		}
		tempDelay = 0

		if tc, ok := conn.(*net.TCPConn); ok {
			period := s.options["TCPKeepAlivePeriod"]
			if period != nil {
				tc.SetKeepAlive(true)
				tc.SetKeepAlivePeriod(period.(time.Duration))
				tc.SetLinger(10)
			}
		}

		conn, ok := s.Plugins.DoPostConnAccept(conn)
		if !ok {
			closeChannel(s, conn)
			continue
		}

		a := newAgent(s, conn)
		s.mu.Lock()
		s.activeConn[conn] = a
		s.mu.Unlock()

		if share.Trace {
			log.Debugf("server accepted an conn: %v", conn.RemoteAddr().String())
		}

		go a.serveConn()
	}
}

// serveByHTTP serves by HTTP.
// if rpcPath is an empty string, use share.DefaultRPCPath.
func (s *Server) serveByHTTP(ln net.Listener, rpcPath string) {
	s.ln = ln

	if rpcPath == "" {
		rpcPath = share.DefaultRPCPath
	}
	http.Handle(rpcPath, s)
	srv := &http.Server{Handler: nil}

	srv.Serve(ln)
}

func parseServerTimeout(ctx *share.Context, req *protocol.Message) context.CancelFunc {
	if req == nil || req.Metadata == nil {
		return nil
	}

	st := req.Metadata[share.ServerTimeout]
	if st == "" {
		return nil
	}

	timeout, err := strconv.ParseInt(st, 10, 64)
	if err != nil {
		return nil
	}

	newCtx, cancel := context.WithTimeout(ctx.Context, time.Duration(timeout)*time.Millisecond)
	ctx.Context = newCtx
	return cancel
}

func isShutdown(s *Server) bool {
	return atomic.LoadInt32(&s.inShutdown) == 1
}

func closeChannel(s *Server, conn net.Conn) {
	s.mu.Lock()
	agent := s.activeConn[conn]
	agent.OnClose()
	delete(s.activeConn, conn)
	s.mu.Unlock()
	conn.Close()
}

func (s *Server) readRequest(ctx context.Context, r io.Reader) (req *protocol.Message, err error) {
	err = s.Plugins.DoPreReadRequest(ctx)
	if err != nil {
		return nil, err
	}
	// pool req?
	req = protocol.GetPooledMsg()
	err = req.Decode(r)
	if err == io.EOF {
		return req, err
	}
	perr := s.Plugins.DoPostReadRequest(ctx, req, err)
	if err == nil {
		err = perr
	}
	return req, err
}

func (s *Server) auth(ctx context.Context, req *protocol.Message) error {
	if s.AuthFunc != nil {
		token := req.Metadata[share.AuthKey]
		return s.AuthFunc(ctx, req, token)
	}

	return nil
}

func (s *Server) handleRequest(agent *ConnAgent, ctx context.Context, req *protocol.Message) (res *protocol.Message, err error) {
	serviceName := req.ServicePath
	methodName := req.ServiceMethod

	session := NewSessionFromRpc(req.NodeId, req.SessionId, req.UserId, agent)
	session.server = s
	if conn, ok := ctx.Value(RemoteConnContextKey).(net.Conn); ok {
		session.conn = conn
	}

	res = req.Clone()

	res.SetMessageType(protocol.Response)
	s.serviceMapMu.RLock()
	service := s.serviceMap[serviceName]

	if share.Trace {
		log.Debugf("server get service %+v for an request %+v", service, req)
	}

	s.serviceMapMu.RUnlock()
	if service == nil {
		err = errors.New("rpcx: can't find service " + serviceName)
		return handleError(res, err)
	}
	mtype := service.Method[methodName]
	if mtype == nil {
		if service.Function[methodName] != nil { // check raw functions
			return s.handleRequestForFunction(ctx, req)
		}
		err = errors.New("rpcx: can't find method " + methodName)
		return handleError(res, err)
	}

	argv := argsReplyPools.Get(mtype.ArgType)

	codec := share.Codecs[req.SerializeType()]
	if codec == nil {
		err = fmt.Errorf("can not find codec for %d", req.SerializeType())
		return handleError(res, err)
	}

	err = codec.Decode(req.Payload, argv)
	if err != nil {
		return handleError(res, err)
	}

	replyv := argsReplyPools.Get(mtype.ReplyType)

	argv, err = s.Plugins.DoPreCall(ctx, serviceName, methodName, argv)
	if err != nil {
		argsReplyPools.Put(mtype.ReplyType, replyv)
		return handleError(res, err)
	}

	if mtype.ArgType.Kind() != reflect.Ptr {
		err = service.Call(ctx, mtype, reflect.ValueOf(session), reflect.ValueOf(argv).Elem(), reflect.ValueOf(replyv))
	} else {
		err = service.Call(ctx, mtype, reflect.ValueOf(session), reflect.ValueOf(argv), reflect.ValueOf(replyv))
	}

	if err == nil {
		replyv, err = s.Plugins.DoPostCall(ctx, serviceName, methodName, argv, replyv)
	}

	argsReplyPools.Put(mtype.ArgType, argv)
	if err != nil {
		if replyv != nil {
			data, err := codec.Encode(replyv)
			argsReplyPools.Put(mtype.ReplyType, replyv)
			if err != nil {
				return handleError(res, err)
			}
			res.Payload = data
		}
		argsReplyPools.Put(mtype.ReplyType, replyv)
		return handleError(res, err)
	}

	if !req.IsOneway() {
		data, err := codec.Encode(replyv)
		argsReplyPools.Put(mtype.ReplyType, replyv)
		if err != nil {
			return handleError(res, err)
		}
		res.Payload = data
	} else if replyv != nil {
		argsReplyPools.Put(mtype.ReplyType, replyv)
	}

	if share.Trace {
		log.Debugf("server called service %+v for an request %+v", service, req)
	}

	return res, nil
}

func (s *Server) handleRequestForFunction(ctx context.Context, req *protocol.Message) (res *protocol.Message, err error) {
	res = req.Clone()

	res.SetMessageType(protocol.Response)

	serviceName := req.ServicePath
	methodName := req.ServiceMethod
	s.serviceMapMu.RLock()
	service := s.serviceMap[serviceName]
	s.serviceMapMu.RUnlock()
	if service == nil {
		err = errors.New("rpcx: can't find service  for func raw function")
		return handleError(res, err)
	}
	mtype := service.Function[methodName]
	if mtype == nil {
		err = errors.New("rpcx: can't find method " + methodName)
		return handleError(res, err)
	}

	argv := argsReplyPools.Get(mtype.ArgType)

	codec := share.Codecs[req.SerializeType()]
	if codec == nil {
		err = fmt.Errorf("can not find codec for %d", req.SerializeType())
		return handleError(res, err)
	}

	err = codec.Decode(req.Payload, argv)
	if err != nil {
		return handleError(res, err)
	}

	replyv := argsReplyPools.Get(mtype.ReplyType)

	if mtype.ArgType.Kind() != reflect.Ptr {
		err = service.CallForFunction(ctx, mtype, reflect.ValueOf(argv).Elem(), reflect.ValueOf(replyv))
	} else {
		err = service.CallForFunction(ctx, mtype, reflect.ValueOf(argv), reflect.ValueOf(replyv))
	}

	argsReplyPools.Put(mtype.ArgType, argv)

	if err != nil {
		argsReplyPools.Put(mtype.ReplyType, replyv)
		return handleError(res, err)
	}

	if !req.IsOneway() {
		data, err := codec.Encode(replyv)
		argsReplyPools.Put(mtype.ReplyType, replyv)
		if err != nil {
			return handleError(res, err)
		}
		res.Payload = data
	} else if replyv != nil {
		argsReplyPools.Put(mtype.ReplyType, replyv)
	}

	return res, nil
}

func handleError(res *protocol.Message, err error) (*protocol.Message, error) {
	res.SetMessageStatusType(protocol.Error)
	if res.Metadata == nil {
		res.Metadata = make(map[string]string)
	}
	res.Metadata[protocol.ServiceError] = err.Error()
	return res, err
}

// Can connect to RPC service using HTTP CONNECT to rpcPath.
var connected = "200 Connected to rpcx"

// ServeHTTP implements an http.Handler that answers RPC requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodConnect {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		io.WriteString(w, "405 must CONNECT\n")
		return
	}
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Info("rpc hijacking ", req.RemoteAddr, ": ", err.Error())
		return
	}
	io.WriteString(conn, "HTTP/1.0 "+connected+"\n\n")

	a := newAgent(s, conn)
	s.mu.Lock()
	s.activeConn[conn] = a
	s.mu.Unlock()

	a.serveConn()
}

// Close immediately closes all active net.Listeners.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	for c := range s.activeConn {
		c.Close()
		delete(s.activeConn, c)
		s.Plugins.DoPostConnClose(c)
	}
	s.closeDoneChanLocked()
	return err
}

// RegisterOnShutdown registers a function to call on Shutdown.
// This can be used to gracefully shutdown connections.
func (s *Server) RegisterOnShutdown(f func(s *Server)) {
	s.mu.Lock()
	s.onShutdown = append(s.onShutdown, f)
	s.mu.Unlock()
}

// RegisterOnRestart registers a function to call on Restart.
func (s *Server) RegisterOnRestart(f func(s *Server)) {
	s.mu.Lock()
	s.onRestart = append(s.onRestart, f)
	s.mu.Unlock()
}

var shutdownPollInterval = 1000 * time.Millisecond

// Shutdown gracefully shuts down the server without interrupting any
// active connections. Shutdown works by first closing the
// listener, then closing all idle connections, and then waiting
// indefinitely for connections to return to idle and then shut down.
// If the provided context expires before the shutdown is complete,
// Shutdown returns the context's error, otherwise it returns any
// error returned from closing the Server's underlying Listener.
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	if atomic.CompareAndSwapInt32(&s.inShutdown, 0, 1) {
		log.Info("shutdown begin")

		s.mu.Lock()
		s.ln.Close()
		for conn := range s.activeConn {
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				tcpConn.CloseRead()
			}
		}
		s.mu.Unlock()

		// wait all in-processing requests finish.
		ticker := time.NewTicker(shutdownPollInterval)
		defer ticker.Stop()
	outer:
		for {
			if s.checkProcessMsg() {
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
				break outer
			case <-ticker.C:
			}
		}

		if s.gatewayHTTPServer != nil {
			if err := s.closeHTTP1APIGateway(ctx); err != nil {
				log.Warnf("failed to close gateway: %v", err)
			} else {
				log.Info("closed gateway")
			}
		}

		s.mu.Lock()
		for conn := range s.activeConn {
			conn.Close()
			delete(s.activeConn, conn)
			s.Plugins.DoPostConnClose(conn)
		}
		s.closeDoneChanLocked()
		s.mu.Unlock()

		log.Info("shutdown end")

	}
	return err
}

// Restart restarts this server gracefully.
// It starts a new rpcx server with the same port with SO_REUSEPORT socket option,
// and shutdown this rpcx server gracefully.
func (s *Server) Restart(ctx context.Context) error {
	pid, err := s.startProcess()
	if err != nil {
		return err
	}
	log.Infof("restart a new rpcx server: %d", pid)

	// TODO: is it necessary?
	time.Sleep(3 * time.Second)
	return s.Shutdown(ctx)
}

func (s *Server) startProcess() (int, error) {
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return 0, err
	}

	// Pass on the environment and replace the old count key with the new one.
	var env []string
	env = append(env, os.Environ()...)

	originalWD, _ := os.Getwd()
	allFiles := []*os.File{os.Stdin, os.Stdout, os.Stderr}
	process, err := os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   originalWD,
		Env:   env,
		Files: allFiles,
	})
	if err != nil {
		return 0, err
	}
	return process.Pid, nil
}

func (s *Server) checkProcessMsg() bool {
	size := atomic.LoadInt32(&s.handlerMsgNum)
	log.Info("need handle in-processing msg size:", size)
	return size == 0
}

func (s *Server) closeDoneChanLocked() {
	select {
	case <-s.doneChan:
		// Already closed. Don't close again.
	default:
		// Safe to close here. We're the only closer, guarded
		// by s.mu.RegisterName
		close(s.doneChan)
	}
}

var ip4Reg = regexp.MustCompile(`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$`)

func validIP4(ipAddress string) bool {
	ipAddress = strings.Trim(ipAddress, " ")
	i := strings.LastIndex(ipAddress, ":")
	ipAddress = ipAddress[:i] // remove port

	return ip4Reg.MatchString(ipAddress)
}
