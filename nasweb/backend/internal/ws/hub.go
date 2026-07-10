// Package ws implementa un hub WebSocket per notificare al frontend eventi in
// tempo reale: avanzamento operazioni file, stato/ricostruzione RAID, ecc.
package ws

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event è il messaggio inviato ai client.
type Event struct {
	Type    string `json:"type"`    // es. "raid.status", "file.progress"
	Payload any    `json:"payload"` // dati specifici dell'evento
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub gestisce i client connessi e il broadcast degli eventi.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*client]struct{}
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]struct{}),
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 64),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default: // client lento: lo si scarta per non bloccare l'hub
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Emit invia un evento a tutti i client connessi.
func (h *Hub) Emit(eventType string, payload any) {
	b, err := json.Marshal(Event{Type: eventType, Payload: payload})
	if err != nil {
		slog.Error("marshal evento ws", "err", err)
		return
	}
	select {
	case h.broadcast <- b:
	default:
		slog.Warn("buffer broadcast pieno, evento scartato", "type", eventType)
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// Same-origin: il check su Origin va affinato in produzione.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ServeWS è l'handler HTTP che effettua l'upgrade a WebSocket.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("upgrade ws fallito", "err", err)
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 32)}
	h.register <- c

	go c.writePump()
	c.readPump(h)
}

func (c *client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (c *client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
