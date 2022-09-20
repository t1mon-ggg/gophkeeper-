package websockets

import (
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/t1mon-ggg/gophkeeper/pkg/helpers"
	"github.com/t1mon-ggg/gophkeeper/pkg/logging"
	"github.com/t1mon-ggg/gophkeeper/pkg/logging/zerolog"
	"github.com/t1mon-ggg/gophkeeper/pkg/models"
)

var (
	upgrader = websocket.Upgrader{}
	_msgs    *channels
	_mux     *sync.Mutex
)

type observer struct {
	vault   string
	token   string
	signal  chan struct{}
	context echo.Context
	ws      *websocket.Conn
	log     logging.Logger
}

type channels struct {
	log     logging.Logger
	signal  chan struct{}
	clients []*wsClients
}

type wsClients struct {
	Vault    string
	Channels map[string]chan models.Message
}

func init() {
	_msgs = new(channels)
	_mux = new(sync.Mutex)

}

func (wsc *channels) Cleanup() []*wsClients {
	return wsc.clients
}

func (wsc *channels) Find(vault string) (map[string]chan models.Message, bool) {
	if len(wsc.clients) == 0 {
		return nil, false
	}
	for _, v := range wsc.clients {
		if vault == v.Vault {
			wsc.log.Trace(nil, "notify find. vault found")
			if len(v.Channels) == 0 {
				wsc.log.Trace(nil, "notify find. no channels availible")
				_mux.Lock()
				chs := make(map[string]chan models.Message)
				v.Channels = chs
				_mux.Unlock()
			}
			return v.Channels, true
		}
	}
	wsc.log.Trace(nil, "notify find. no results")
	return nil, false
}

func (wsc *channels) Add(vault, token string) chan models.Message {
	if wsc.log == nil {
		wsc.log = zerolog.New().WithPrefix("websocket-notify")
	}
	wsc.log.Trace(nil, "add ", vault, " ", token)
	if chs, ok := wsc.Find(vault); ok {
		wsc.log.Trace(nil, "vault found. adding")
		_mux.Lock()
		ch := make(chan models.Message, 1)
		chs[token] = ch
		_mux.Unlock()
		return ch
	}
	wsc.log.Trace(nil, "not found. creatind")
	_mux.Lock()
	chs := make(map[string]chan models.Message)
	v := wsClients{Vault: vault, Channels: chs}
	wsc.clients = append(wsc.clients, &v)
	_mux.Unlock()
	wsc.log.Trace(nil, "add recurcy")
	ch := wsc.Add(vault, token)
	return ch
}
func (wsc *channels) Notify(vault, token string, msg models.Message) {
	wsc.log.Trace(nil, "notify action. searching")
	for _, vv := range wsc.clients {
		if vv.Vault == vault {
			wsc.log.Trace(nil, "notify action. vault found")
			for k, v := range vv.Channels {
				if k == token {
					wsc.log.Trace(nil, "self skipping")
					continue
				}
				go func(ch chan models.Message) {
					wsc.log.Trace(nil, "notify action")
					ch <- msg
				}(v)
			}
		}
	}
}

func GetMsgChan() *channels {
	return _msgs
}

func GetMutex() *sync.Mutex {
	return _mux
}

func New(c echo.Context) error {
	o := new(observer)
	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	o.ws = ws
	o.signal = make(chan struct{})
	o.context = c
	o.log = zerolog.New().WithPrefix("websocket")
	token, err := c.Cookie("token")
	if err != nil {
		o.log.Error(err, "connection with wrong token")
		return err
	}
	o.token = token.Value
	name, err := helpers.GetNameFromToken(token.Value)
	if err != nil {
		o.log.Error(err, "connection with wrong token")
		return err
	}
	o.vault = name
	err = o.Start()
	if err != nil {
		if websocket.IsCloseError(err, 1000) {
			o.log.Debug(nil, "websocket closed normally by signal from client")
			return nil
		}
		o.log.Error(err, "websocket can not be created")
		return err
	}
	return nil
}

func (o *observer) Start() error {
	ch := _msgs.Add(o.vault, o.token)
	defer o.ws.Close()
	for {
		select {
		case <-o.signal:
			o.log.Info(nil, "stop signal recieved")
			return nil
		case msg := <-ch:
			err := o.ws.WriteJSON(msg)
			if err != nil {
				o.log.Error(err, "websocket write message failed")
				return err
			}
		default:
			_, msg, err := o.ws.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, 1000) {
					return nil
				}
				o.log.Error(err, "websocket read failed")
				return err
			}
			if string(msg) == "ping" {
				err := o.ws.WriteMessage(websocket.TextMessage, []byte("pong"))
				if err != nil {
					o.log.Error(err, "websocket write pong failed")
					return err
				}
			}
		}
	}
}

func (o *observer) Close() {
	o.log.Debug(nil, "websocket hadler closing")
	close(o.signal)
	close(_msgs.signal)
}