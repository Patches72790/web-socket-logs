package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
	"web-sockets/util"

	"github.com/gorilla/websocket"
)

const (
	write_wait  = 20 * time.Second
	file_period = 10 * time.Millisecond
)

var (
	upgrader = websocket.Upgrader{}
	WS_ID    = WebSocketID{}
)

type WebSocketID struct {
	next_id uint
}

func (i *WebSocketID) NewID() uint {
	id := i.next_id
	i.next_id += 1
	return id
}

// TODO => Consider a "MODE" enum that would allow
// for different types of handling scenarios
// such as search, read, file_search, et cetera
type WebSocketSession struct {
	id           uint
	conn         *websocket.Conn
	filename     string
	read_size    uint64
	search_mode  bool
	search_chan  chan string
	log_seek_pos struct {
		sync.Mutex
		offset int64
	}
}

func (s *WebSocketSession) Close() {
	s.conn.Close()
	close(s.search_chan)
}

func (s *WebSocketSession) ReadCommand() (*ClientCommand, error) {
	msg_type, b, err := s.conn.ReadMessage()

	// Received close signal
	if msg_type == -1 {
		return &ClientCommand{Type: "CLOSE", Message: ""}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Error reading client message %s", err)
	}

	var command ClientCommand
	err = json.Unmarshal(b, &command)
	if err != nil {
		return nil, fmt.Errorf("Error unmarshalling json command %s", err)
	}

	return &command, nil
}

func NewWebSocketSession(conn *websocket.Conn, filename string) *WebSocketSession {
	return &WebSocketSession{
		id:          WS_ID.NewID(),
		conn:        conn,
		filename:    filename,
		read_size:   8192,
		search_chan: make(chan string),
	}
}

/*
The commands that may be processed by the web socket reader handler.

	SEARCH_CTL  [ON | OFF]
	KEY         [j | k | J | K]
	SEARCH_VAL  [ string ]
	CLOSE
*/
type ClientCommand struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func ws_reader(session *WebSocketSession) {
	for {
		command, err := session.ReadCommand()
		if err != nil {
			panic(fmt.Errorf("Error reading client command %s", err))
		}

		switch command.Type {
		case "KEY":
			switch command.Message {
			case "J":
				// go to bottom
				session.log_seek_pos.Lock()
				file, err := os.Stat(session.filename)
				if err != nil {
					log.Println("Error getting file info", err)
					session.log_seek_pos.Unlock()
					break
				}

				session.log_seek_pos.offset = file.Size() - int64(session.read_size)
				log.Printf("Set offset to %d\n", session.log_seek_pos.offset)
				session.log_seek_pos.Unlock()
				continue
			case "K":
				// go to top
				session.log_seek_pos.Lock()
				session.log_seek_pos.offset = 0
				log.Printf("Set offset to %d\n", session.log_seek_pos.offset)
				session.log_seek_pos.Unlock()
				continue
			case "j":
				session.log_seek_pos.Lock()
				file, err := os.Stat(session.filename)
				if err != nil {
					log.Println("Error getting file info", err)
					session.log_seek_pos.Unlock()
					break
				}

				if math.Abs(float64(file.Size()-session.log_seek_pos.offset)) < float64(session.read_size) {
					log.Printf("EOF reached - skipping offset increment")
					session.log_seek_pos.Unlock()
					continue
				}

				session.log_seek_pos.offset += int64(session.read_size)
				log.Printf("Set offset to %d\n", session.log_seek_pos.offset)
				session.log_seek_pos.Unlock()
				continue
			case "k":
				session.log_seek_pos.Lock()
				if session.log_seek_pos.offset == 0 {
					log.Println("Offset at 0, continuing")
					session.log_seek_pos.Unlock()
					continue
				}
				session.log_seek_pos.offset -= int64(session.read_size)
				// don't underflow offset
				if session.log_seek_pos.offset < 0 {
					session.log_seek_pos.offset = 0
				}
				log.Printf("Set offset to %d\n", session.log_seek_pos.offset)
				session.log_seek_pos.Unlock()
				continue
			}
		case "SEARCH_CTL":
			// TODO => Refactor to use timestamps perhaps to avoid stale searches
			switch command.Message {
			case "ON":
				log.Println("Setting search mode")
				session.search_mode = true
			case "OFF":
				log.Println("Clearing search mode")
				session.search_mode = false
				session.search_chan <- ""
			}
		case "SEARCH_VAL":
			log.Printf("Processing search value: %s", command.Message)
			session.search_chan <- command.Message
			break
		case "CLOSE":
			log.Println("Received close command from socket. Exiting")
			return
		default:
			log.Printf("Skipping handling unknown command type:\n\n %s\n", command)
		}
	}
}

func ws_writer(session *WebSocketSession) {
	file_ticker := time.NewTicker(file_period)
	defer func() {
		file_ticker.Stop()
	}()

	file, err := os.Open(session.filename)
	defer file.Close()
	if err != nil {
		log.Printf("Error reading file: %s", err)
		return
	}

	var last_read_offset int64 = -1
	for {
		// if search mode is on, wait for search input
		switch session.search_mode {
		case true:
			select {
			// read changes in search channel
			case search_str, ok := <-session.search_chan:

				if !ok {
					log.Println("Error reading search channel")
					return
				}

				log.Println("Read change in search channel", search_str)
				str_matches, err := util.SearchFile(search_str, session.filename)
				if err != nil {
					log.Printf("Error reading file: %s", err)
					return
				}

				err = session.conn.SetWriteDeadline(time.Now().Add(write_wait))
				if err != nil {
					log.Printf("Error setting write limit %s", err)
					return
				}

				err = session.conn.WriteMessage(websocket.TextMessage, []byte(str_matches))
				if err != nil {
					log.Printf("Error writing search message: %s", err)
					return
				}
			}

		default:
			// otherwise, select on the period of the ticker
			select {
			case <-file_ticker.C:
				buf := make([]byte, session.read_size)
				session.log_seek_pos.Lock()

				// check for no change in offset, no need to double read
				if last_read_offset == session.log_seek_pos.offset {
					session.log_seek_pos.Unlock()
					continue
				}

				// seek to current offset from start of file
				offset, err := file.Seek(session.log_seek_pos.offset, io.SeekStart)
				// read read_size of bytes from offset
				n, err := file.ReadAt(buf, offset)
				// keep track of last offset
				last_read_offset = offset

				// error is not EOF or otherwise
				if err != nil && !errors.Is(err, io.EOF) {
					log.Printf("Error reading file: %s", err)
					session.log_seek_pos.Unlock()
					break
				}

				// Reached EOF
				if n == 0 {
					log.Println("Read 0 bytes, continuing")
					session.log_seek_pos.Unlock()
					continue
				}

				session.log_seek_pos.Unlock()

				log.Printf("Read %d bytes", n)

				if buf != nil {
					err := session.conn.SetWriteDeadline(time.Now().Add(write_wait))
					if err != nil {
						log.Printf("Error setting write limit %s", err)
						return
					}

					err = session.conn.WriteMessage(websocket.TextMessage, buf)
					if err != nil {
						log.Printf("Error writing message: %s", err)
						return
					}
				}
			}
		}
	}
}

func log_handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Fatalf("Error upgrading to ws connection: %s", err)
	}

	session := NewWebSocketSession(conn, "log")
	defer func() {
		session.Close()
	}()

	go ws_writer(session)
	ws_reader(session)
}

func index(w http.ResponseWriter, r *http.Request) {
	indexHtml, err := os.ReadFile("index.html")
	if err != nil {
		panic(fmt.Errorf("Error reading template file %s", err))
	}
	templ, err := template.New("").Parse(string(indexHtml))
	if err != nil {
		panic(fmt.Errorf("Error parsing template %s", err))
	}

	templ.Execute(w, struct {
		SocketConn string
	}{SocketConn: "ws://" + r.Host + "/log"})
}

func main() {
	http.HandleFunc("/log", log_handler)
	http.HandleFunc("/", index)
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}
