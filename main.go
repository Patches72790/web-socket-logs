package main

import (
	"errors"
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
	//"github.com/pkg/sftp"
)

const (
	write_wait  = 20 * time.Second
	file_period = 10 * time.Millisecond
)

var (
	upgrader         = websocket.Upgrader{}
	filename  string = "log"
	read_size uint64 = 8192

	log_seek_pos struct {
		sync.Mutex
		offset int64
	}
)

type Message struct {
	msg string
}

func ws_reader(ws *websocket.Conn, search_chan *chan string) {
	for {
		_, b, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Error reading message: %s", err)
			break
		}

		message := string(b)
		switch message {
		case "J":
			// go to bottom
			log_seek_pos.Lock()
			file, err := os.Stat(filename)
			if err != nil {
				log.Println("Error getting file info", err)
				log_seek_pos.Unlock()
				break
			}

			log_seek_pos.offset = file.Size() - int64(read_size)
			log.Printf("Set offset to %d\n", log_seek_pos.offset)
			log_seek_pos.Unlock()
			continue
		case "K":
			// go to top
			log_seek_pos.Lock()
			log_seek_pos.offset = 0
			log.Printf("Set offset to %d\n", log_seek_pos.offset)
			log_seek_pos.Unlock()
			continue
		case "j":
			log_seek_pos.Lock()
			file, err := os.Stat(filename)
			if err != nil {
				log.Println("Error getting file info", err)
				log_seek_pos.Unlock()
				break
			}

			if math.Abs(float64(file.Size()-log_seek_pos.offset)) < float64(read_size) {
				log.Printf("EOF reached - skipping offset increment")
				log_seek_pos.Unlock()
				continue
			}

			log_seek_pos.offset += int64(read_size)
			log.Printf("Set offset to %d\n", log_seek_pos.offset)
			log_seek_pos.Unlock()
			continue
		case "k":
			log_seek_pos.Lock()
			if log_seek_pos.offset == 0 {
				log.Println("Offset at 0, continuing")
				log_seek_pos.Unlock()
				continue
			}
			log_seek_pos.offset -= int64(read_size)
			// don't underflow offset
			if log_seek_pos.offset < 0 {
				log_seek_pos.offset = 0
			}
			log.Printf("Set offset to %d\n", log_seek_pos.offset)
			log_seek_pos.Unlock()
			continue
		default:
			// // TODO => fix and add command for search
			// add search message to channel
			*search_chan <- message
			continue

		}
	}
}

func ws_writer(ws *websocket.Conn, search_chan *chan string) {
	file_ticker := time.NewTicker(file_period)
	defer func() {
		file_ticker.Stop()
	}()

	file, err := os.Open(filename)
	defer file.Close()
	if err != nil {
		log.Printf("Error reading file: %s", err)
		return
	}

	is_search := false
	var last_read_offset int64 = -1
	for {
		select {
		// read changes in search channel
		case search_str, ok := <-*search_chan:

			if !ok {
				log.Println("Error reading search channel")
				return
			}

			log.Println("Read change in search channel", search_str)
			str_matches, err := util.SearchFile(search_str, filename)
			if err != nil {
				log.Printf("Error reading file: %s", err)
				return
			}

			err = ws.SetWriteDeadline(time.Now().Add(write_wait))
			if err != nil {
				log.Printf("Error setting write limit %s", err)
				return
			}

			err = ws.WriteMessage(websocket.TextMessage, []byte(str_matches))
			if err != nil {
				log.Printf("Error writing search message: %s", err)
				return
			}

			is_search = true

			continue
		case <-file_ticker.C:
			buf := make([]byte, read_size)
			log_seek_pos.Lock()

			if is_search {
				log_seek_pos.Unlock()
				continue
			}
			// check for no change in offset, no need to double read
			if last_read_offset == log_seek_pos.offset {
				log_seek_pos.Unlock()
				continue
			}

			// seek to current offset from start of file
			offset, err := file.Seek(log_seek_pos.offset, io.SeekStart)
			// read read_size of bytes from offset
			n, err := file.ReadAt(buf, offset)
			// keep track of last offset
			last_read_offset = offset

			// error is not EOF or otherwise
			if err != nil && !errors.Is(err, io.EOF) {
				log.Printf("Error reading file: %s", err)
				log_seek_pos.Unlock()
				break
			}

			// Reached EOF
			if n == 0 {
				log.Println("Read 0 bytes, continuing")
				log_seek_pos.Unlock()
				continue
			}

			log_seek_pos.Unlock()

			log.Printf("Read %d bytes", n)

			if buf != nil {
				err := ws.SetWriteDeadline(time.Now().Add(write_wait))
				if err != nil {
					log.Printf("Error setting write limit %s", err)
					return
				}

				err = ws.WriteMessage(websocket.TextMessage, buf)
				if err != nil {
					log.Printf("Error writing message: %s", err)
					return
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

	search_chan := make(chan string)

	defer func() {
		conn.Close()
		close(search_chan)
	}()

	go ws_writer(conn, &search_chan)
	ws_reader(conn, &search_chan)
}

func index(w http.ResponseWriter, r *http.Request) {
	homeTemplate.Execute(w, struct {
		SocketConn string
	}{SocketConn: "ws://" + r.Host + "/log"})
}

func main() {

	http.HandleFunc("/log", log_handler)
	http.HandleFunc("/", index)
	log.Fatal(http.ListenAndServe("localhost:8080", nil))
}

var homeTemplate = template.Must(template.New("").Parse(`
<html>
    <body>
        <div id="root">
            <input id="search"/>
            <div id="logs"></div>
        </div>
    </body>

    <script type="text/javascript">
        (() => {
            const logs = document.getElementById("logs")
            const conn = new WebSocket({{.SocketConn}})
            const search = document.getElementById("search")

            conn.onopen = () => {
                console.log("Connection opened!")
            }

            conn.onclose = () => {
                console.log("Connection closed!")
            }

            conn.onmessage = (evt) => {
                // remove children on update
                while (logs.firstChild) {
                    logs.removeChild(logs.lastChild)
                }

                console.log(evt.data.split("\n"))
                evt.data
                    .split("\n")
                    .map(line => {
                        const p = document.createElement("p")
                        p.textContent = line
                        return p
                    })
                    .forEach(p => logs.appendChild(p))
            }

            document.addEventListener("keydown", (e) => {
                console.log(e)
                switch (e.key) {
                    case "j":
                    case "k":
                    case "J":
                    case "K":
                        conn.send(e.key)
                        break
                    case "/":
                        console.log("search focused")
                        search.focus()
                        break
                    case "Enter":
                        conn.send(search.value.replace("/", ""))
                        break
                    default:
                        return
                }
            })

        })()
    </script>
</html>
    `))
