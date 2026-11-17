package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type wsMessage struct {
	Type      string          `json:"type"`
	ProjectID int64           `json:"project_id,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type wsConnection struct {
	conn      *websocket.Conn
	userID    int64
	projects  map[int64]bool
	send      chan wsMessage
	hub       *wsHub
	writeMu   sync.Mutex
}

type wsHub struct {
	connections map[*wsConnection]bool
	broadcast   chan wsMessage
	register    chan *wsConnection
	unregister  chan *wsConnection
	mu          sync.RWMutex
	db          authRepository
}

func newWSHub(db authRepository) *wsHub {
	return &wsHub{
		connections: make(map[*wsConnection]bool),
		broadcast:   make(chan wsMessage, 256),
		register:    make(chan *wsConnection),
		unregister:  make(chan *wsConnection),
		db:          db,
	}
}

func (h *wsHub) run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.connections[conn] = true
			h.mu.Unlock()
		case conn := <-h.unregister:
			h.mu.Lock()
			if h.connections[conn] {
				delete(h.connections, conn)
				close(conn.send)
			}
			h.mu.Unlock()
		case msg := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.connections {
				if msg.ProjectID == 0 || conn.projects[msg.ProjectID] {
					select {
					case conn.send <- msg:
					default:
						go func(c *wsConnection) {
							h.unregister <- c
						}(conn)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *wsHub) Broadcast(msg wsMessage) {
	select {
	case h.broadcast <- msg:
	default:
		log.Printf("broadcast channel full, dropping message type=%s project=%d", msg.Type, msg.ProjectID)
	}
}

func (c *wsConnection) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("ws read error user=%d: %v", c.userID, err)
			}
			break
		}
	}
}

func (c *wsConnection) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.writeMu.Lock()
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				c.writeMu.Unlock()
				return
			}
			if err := c.conn.WriteJSON(msg); err != nil {
				c.writeMu.Unlock()
				return
			}
			c.writeMu.Unlock()
		case <-ticker.C:
			c.writeMu.Lock()
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.writeMu.Unlock()
				return
			}
			c.writeMu.Unlock()
		}
	}
}

func (s *v1Server) wsHandler(hub *wsHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		if token == "" {
			http.Error(w, "token required", http.StatusUnauthorized)
			return
		}
		user, err := s.authRepo.UserForSession(r.Context(), tokenDigest(token), time.Now())
		if err != nil {
			http.Error(w, "invalid or expired session", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed user=%d: %v", user.ID, err)
			return
		}
		projects, err := s.projects.ListForUser(r.Context(), user.ID)
		if err != nil {
			conn.Close()
			log.Printf("ws list projects failed user=%d: %v", user.ID, err)
			return
		}
		projectMap := make(map[int64]bool, len(projects))
		for _, p := range projects {
			projectMap[p.ID] = true
		}
		wsConn := &wsConnection{
			conn:     conn,
			userID:   user.ID,
			projects: projectMap,
			send:     make(chan wsMessage, 256),
			hub:      hub,
		}
		hub.register <- wsConn
		go wsConn.writePump()
		wsConn.readPump()
	}
}
