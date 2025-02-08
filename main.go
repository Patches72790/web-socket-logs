package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

	"web-sockets/sftp"
	"web-sockets/ws"

	"github.com/gorilla/websocket"
)

var (
	upgrader = websocket.Upgrader{}
)

func log_handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Fatalf("Error upgrading to ws connection: %s", err)
	}

	sftpSession, err := sftp.NewSftpSession("test-mumaa-adm", "test.mumaa-admin.doit.wisc.edu")
	if err != nil {
		log.Fatalf("Error getting new sftp session %s", err)
	}
	session := ws.NewWebSocketSession(conn, "logs/tomcat/mumaa/new-sam-service.log", sftpSession)

	defer func() {
		session.Close()
	}()

	go ws.WsWriter(session)
	ws.WsReader(session)
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
