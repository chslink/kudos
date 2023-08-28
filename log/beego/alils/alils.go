package alils

//
//const (
//	// CacheSize set the flush size
//	CacheSize int = 64
//	// Delimiter define the topic delimiter
//	Delimiter string = "##"
//)
//
//// Config is the Config for Ali Log
//type Config struct {
//	Project   string   `json:"project"`
//	Endpoint  string   `json:"endpoint"`
//	KeyID     string   `json:"key_id"`
//	KeySecret string   `json:"key_secret"`
//	LogStore  string   `json:"log_store"`
//	Topics    []string `json:"topics"`
//	Source    string   `json:"source"`
//	Level     int      `json:"level"`
//	FlushWhen int      `json:"flush_when"`
//}
//
//// aliLSWriter implements LoggerInterface.
//// it writes messages in keep-live tcp connection.
//type aliLSWriter struct {
//	store    *LogStore
//	group    []*LogGroup
//	withMap  bool
//	groupMap map[string]*LogGroup
//	lock     *sync.Mutex
//	Config
//}
//
//// NewAliLS create a new Logger
//func NewAliLS() logs.Logger {
//	alils := new(aliLSWriter)
//	alils.Level = logs.LevelTrace
//	return alils
//}
//
//// Init parse config and init struct
//func (c *aliLSWriter) Init(jsonConfig string) (err error) {
//
//	json.Unmarshal([]byte(jsonConfig), c)
//
//	if c.FlushWhen > CacheSize {
//		c.FlushWhen = CacheSize
//	}
//
//	prj := &LogProject{
//		Name:            c.Project,
//		Endpoint:        c.Endpoint,
//		AccessKeyID:     c.KeyID,
//		AccessKeySecret: c.KeySecret,
//	}
//
//	c.store, err = prj.GetLogStore(c.LogStore)
//	if err != nil {
//		return err
//	}
//
//	// Create default Log Group
//	c.group = append(c.group, &LogGroup{
//		Topic:  String(""),
//		Source: String(c.Source),
//		Logs:   make([]*Log, 0, c.FlushWhen),
//	})
//
//	// Create other Log Group
//	c.groupMap = make(map[string]*LogGroup)
//	for _, topic := range c.Topics {
//
//		lg := &LogGroup{
//			Topic:  String(topic),
//			Source: String(c.Source),
//			Logs:   make([]*Log, 0, c.FlushWhen),
//		}
//
//		c.group = append(c.group, lg)
//		c.groupMap[topic] = lg
//	}
//
//	if len(c.group) == 1 {
//		c.withMap = false
//	} else {
//		c.withMap = true
//	}
//
//	c.lock = &sync.Mutex{}
//
//	return nil
//}
//
//// WriteMsg write message in connection.
//// if connection is down, try to re-connect.
//func (c *aliLSWriter) WriteMsg(when time.Time, msg string, level int) (err error) {
//
//	if level > c.Level {
//		return nil
//	}
//
//	var topic string
//	var content string
//	var lg *LogGroup
//	if c.withMap {
//
//		// Topic，LogGroup
//		strs := strings.SplitN(msg, Delimiter, 2)
//		if len(strs) == 2 {
//			pos := strings.LastIndex(strs[0], " ")
//			topic = strs[0][pos+1 : len(strs[0])]
//			content = strs[0][0:pos] + strs[1]
//			lg = c.groupMap[topic]
//		}
//
//		// send to empty Topic
//		if lg == nil {
//			content = msg
//			lg = c.group[0]
//		}
//	} else {
//		content = msg
//		lg = c.group[0]
//	}
//
//	c1 := &LogContent{
//		Key:   String("msg"),
//		Value: String(content),
//	}
//
//	l := &Log{
//		Time: Uint32(uint32(when.Unix())),
//		Contents: []*LogContent{
//			c1,
//		},
//	}
//
//	c.lock.Lock()
//	lg.Logs = append(lg.Logs, l)
//	c.lock.Unlock()
//
//	if len(lg.Logs) >= c.FlushWhen {
//		c.flush(lg)
//	}
//
//	return nil
//}
//
//// Flush implementing method. empty.
//func (c *aliLSWriter) Flush() {
//
//	// flush all group
//	for _, lg := range c.group {
//		c.flush(lg)
//	}
//}
//
//// Destroy destroy connection writer and close tcp listener.
//func (c *aliLSWriter) Destroy() {
//}
//
//func (c *aliLSWriter) flush(lg *LogGroup) {
//
//	c.lock.Lock()
//	defer c.lock.Unlock()
//	err := c.store.PutLogs(lg)
//	if err != nil {
//		return
//	}
//
//	lg.Logs = make([]*Log, 0, c.FlushWhen)
//}
//
//func init() {
//	logs.Register(logs.AdapterAliLS, NewAliLS)
//}
//
//// Bool is a helper routine that allocates a new bool value
//// to store v and returns a pointer to it.
//func Bool(v bool) *bool {
//	return &v
//}
//
//// Int32 is a helper routine that allocates a new int32 value
//// to store v and returns a pointer to it.
//func Int32(v int32) *int32 {
//	return &v
//}
//
//// Int is a helper routine that allocates a new int32 value
//// to store v and returns a pointer to it, but unlike Int32
//// its argument value is an int.
//func Int(v int) *int32 {
//	p := new(int32)
//	*p = int32(v)
//	return p
//}
//
//// Int64 is a helper routine that allocates a new int64 value
//// to store v and returns a pointer to it.
//func Int64(v int64) *int64 {
//	return &v
//}
//
//// Float32 is a helper routine that allocates a new float32 value
//// to store v and returns a pointer to it.
//func Float32(v float32) *float32 {
//	return &v
//}
//
//// Float64 is a helper routine that allocates a new float64 value
//// to store v and returns a pointer to it.
//func Float64(v float64) *float64 {
//	return &v
//}
//
//// Uint32 is a helper routine that allocates a new uint32 value
//// to store v and returns a pointer to it.
//func Uint32(v uint32) *uint32 {
//	return &v
//}
//
//// Uint64 is a helper routine that allocates a new uint64 value
//// to store v and returns a pointer to it.
//func Uint64(v uint64) *uint64 {
//	return &v
//}
//
//// String is a helper routine that allocates a new string value
//// to store v and returns a pointer to it.
//func String(v string) *string {
//	return &v
//}
