package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"time"
	"web-sockets/sftp"
	"web-sockets/util"

	"github.com/gorilla/websocket"
)

const WRITE_WAIT = 20 * time.Second

var WS_ID = WebSocketID{}

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
	id               uint
	conn             *websocket.Conn
	sftp             *sftp.SftpSession
	filename         string
	read_size        uint64
	search_mode      bool
	mode_chan        chan string
	search_chan      chan string
	file_offset_chan chan int64
}

func (s *WebSocketSession) Close() {
	s.conn.Close()
	close(s.search_chan)
	close(s.file_offset_chan)
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

func NewWebSocketSession(conn *websocket.Conn, filename string, session *sftp.SftpSession) *WebSocketSession {
	return &WebSocketSession{
		id:               WS_ID.NewID(),
		conn:             conn,
		filename:         filename,
		read_size:        8192,
		mode_chan:        make(chan string),
		search_chan:      make(chan string),
		file_offset_chan: make(chan int64),
		sftp:             session,
	}
}

/*
The commands that may be processed by the web socket reader handler.

	MODE_CTL  [SEARCH | SCROLL]
	KEY         [j | k | J | K]
	SEARCH_VAL  [ string ]
	CLOSE
*/
type ClientCommand struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func WsReader(session *WebSocketSession) {
	for {
		command, err := session.ReadCommand()
		if err != nil {
			panic(fmt.Errorf("Error reading client command %s", err))
		}

		switch command.Type {
		case "KEY":
			switch command.Message {
			case "J":
				log.Println("TODO GOTO BOTTOM")
			case "K":
				log.Println("TODO GOTO TOP")
			case "j":
				session.file_offset_chan <- int64(session.read_size)
			case "k":
				session.file_offset_chan <- -int64(session.read_size)
			}
		case "MODE_CTL":
			switch command.Message {
			case "SEARCH":
				log.Println("Setting search mode")
				session.mode_chan <- "SEARCH"
			case "SCROLL":
				log.Println("Setting scroll mode")
				session.mode_chan <- "SCROLL"
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

func WsWriter(session *WebSocketSession) {
	file, err := session.sftp.Open(session.filename)
	defer file.Close()
	if err != nil {
		log.Printf("Error reading file: %s", err)
		return
	}

	current_mode := "SCROLL"
	for {
		select {
		case mode_chan, ok := <-session.mode_chan:
			if !ok {
				log.Println("Error reading mode channel")
				return
			}
			log.Printf("Setting mode to %s", mode_chan)
			current_mode = mode_chan

		// read changes in search channel
		case search_str, ok := <-session.search_chan:
			if current_mode != "SEARCH" {
				continue
			}

			if !ok {
				log.Println("Error reading search channel")
				return
			}

			log.Println("Read change in search channel", search_str)
			str_matches, err := util.SearchFile(search_str, file)
			if err != nil {
				log.Printf("Error reading file: %s", err)
				return
			}

			err = session.conn.SetWriteDeadline(time.Now().Add(WRITE_WAIT))
			if err != nil {
				log.Printf("Error setting write limit %s", err)
				return
			}

			err = session.conn.WriteMessage(websocket.TextMessage, []byte(str_matches))
			if err != nil {
				log.Printf("Error writing search message: %s", err)
				return
			}

		// otherwise, select on the period of the ticker
		case file_offset, ok := <-session.file_offset_chan:
			if current_mode != "SCROLL" {
				continue
			}

			if !ok {
				log.Println("Error reading file offset channel")
				return
			}

			file_info, err := file.Stat()
			if err != nil {
				log.Printf("Error calling fstat %s", err)
				return
			}
			current_offset, err := file.Seek(0, io.SeekCurrent)
			if err != nil {
				log.Printf("Error calling fseek %s", err)
				return
			}

			// case 1) change offset < 0 and current offset is at BOF (dont read before beginning of file)
			if file_offset < 0 && current_offset == 0 {
				log.Println("Not seeking file since at beginning of file")
				continue
			}

			// case 2) change offset > 0 and current offset is at EOF (dont read after EOF)
			if current_offset+file_offset > file_info.Size() {
				log.Printf("Not reading file since at EOF offset %d\n", current_offset)
				continue
			}

			// case 3) -inf < change offset < inf and current offset is at MOF (otherwise, read either direction)
			offset, err := file.Seek(file_offset, io.SeekCurrent)
			if err != nil {
				log.Printf("Error calling fseek %s", err)
				return
			}

			// read read_size of bytes from offset
			buf := make([]byte, session.read_size)
			n, err := file.ReadAt(buf, offset)

			// error is not EOF or otherwise
			if err != nil && !errors.Is(err, io.EOF) {
				log.Printf("Error reading file (NOT EOF) %s", err)
				return
			}

			log.Printf("Read %d bytes at offset %d", n, offset)

			if buf != nil {
				err := session.conn.SetWriteDeadline(time.Now().Add(WRITE_WAIT))
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
