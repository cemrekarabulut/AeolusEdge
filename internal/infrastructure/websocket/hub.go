// Package websocket — gerçek zamanlı anomali bildirimi için WebSocket hub'ı.
//
// DÜZELTİLEN HATALAR (v2):
//   1. Hub.Run() artık context alıyor → graceful shutdown çalışıyor
//   2. IsUnexpectedCloseError loglanıyor (önceki: _ = err ile yutuluyordu)
//   3. ActiveClientCount() atomic ile lock-free — Prometheus'a bağlanabilir
package websocket

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 54 * time.Second
	maxMessageSize = 4096
	sendBufSize    = 64
)

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

type Hub struct {
	clients     map[*Client]struct{}
	mu          sync.RWMutex
	register    chan *Client
	unregister  chan *Client
	broadcast   chan []byte
	logger      *slog.Logger
	activeCount atomic.Int64
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client),
		unregister: make(chan *Client, 16),
		broadcast:  make(chan []byte, 256),
		logger:     logger,
	}
}

// Run — context-aware event loop. DÜZELTME: önceki sürümde context yoktu,
// SIGTERM sonrası hub goroutine sonsuza dek asılı kalıyordu.
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info("websocket hub başlatıldı")
	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			h.logger.Info("websocket hub kapatıldı")
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			count := int64(len(h.clients))
			h.mu.Unlock()
			h.activeCount.Store(count)
			h.logger.Info("ws client bağlandı", slog.Int64("active", count))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			count := int64(len(h.clients))
			h.mu.Unlock()
			h.activeCount.Store(count)
			h.logger.Info("ws client ayrıldı", slog.Int64("active", count))

		case msg, ok := <-h.broadcast:
			if !ok {
				return
			}
			h.broadcastToAll(msg)
		}
	}
}

func (h *Hub) broadcastToAll(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.clients {
		select {
		case client.send <- msg:
		default:
			h.logger.Warn("yavaş ws client, frame atlandı")
		}
	}
}

func (h *Hub) BroadcastJSON(data []byte) {
	buf := make([]byte, len(data))
	copy(buf, data)
	select {
	case h.broadcast <- buf:
	default:
		h.logger.Warn("broadcast kanalı dolu, mesaj atlandı")
	}
}

// ActiveClientCount — lock-free, Prometheus gauge için.
func (h *Hub) ActiveClientCount() int64 {
	return h.activeCount.Load()
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func ServeWS(hub *Hub, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.ErrorContext(r.Context(), "ws upgrade başarısız",
				slog.String("err", err.Error()),
				slog.String("remote", r.RemoteAddr),
			)
			return
		}
		client := &Client{hub: hub, conn: conn, send: make(chan []byte, sendBufSize)}
		hub.register <- client
		go client.writePump(logger)
		go client.readPump(logger)
	}
}

func (c *Client) writePump(logger *slog.Logger) {
	ticker := time.NewTicker(pingPeriod)
	defer func() { ticker.Stop(); c.conn.Close() }()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				logger.Warn("ws yazma hatası", slog.String("err", err.Error()))
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump — DÜZELTME: önceki sürümde unexpected close _ = err ile yutuluyordu.
func (c *Client) readPump(logger *slog.Logger) {
	defer func() { c.hub.unregister <- c; c.conn.Close() }()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				logger.Warn("ws beklenmedik kapanış", slog.String("err", err.Error()))
			}
			return
		}
	}
}
