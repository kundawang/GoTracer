package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sync"
	"time"

	"strings"

	"runtime"

	"github.com/DheerendraRathor/GoTracer/models"
	"github.com/DheerendraRathor/GoTracer/net/constants"
	"github.com/DheerendraRathor/GoTracer/tracer"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	pongTimeout = 60 * time.Second
	pongWait    = 20 * time.Second
	pingMessage = "a"
)

type RenderingClient struct {
	Conn                *websocket.Conn
	Results             chan *models.Pixel
	IsTracingInProgress bool
	CloseChan           chan bool
	OperationId         string
	PongChannel         chan bool
	WaitGroup           sync.WaitGroup
	// writeMutex serialises every frame written to Conn: the ping goroutine
	// and the result sender otherwise interleave writes on one websocket and
	// corrupt random frames, producing black pixels in the assembled image.
	writeMutex sync.Mutex
}

// writeJSON sends one frame while holding the connection write lock.
func (c *RenderingClient) writeJSON(message interface{}) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	return c.Conn.WriteJSON(message)
}

func (c *RenderingClient) Initialize() {
	c.WaitGroup = sync.WaitGroup{}

	c.Conn.SetPongHandler(
		func(message string) error {
			c.PongChannel <- true
			return nil
		},
	)

	go c.pingThread()

	c.WaitGroup.Add(2)

	go c.ReadHandler()
	go c.ResultSender()

	c.WaitGroup.Wait()
}

var addr = flag.String("addr", "", "http service address")

var upgrader = websocket.Upgrader{}
var mutex = &sync.Mutex{}

var activeConn *websocket.Conn = nil

func (c *RenderingClient) pingThread() {
	conn := c.Conn
	ticker := time.NewTicker(pongWait)
	timer := time.NewTimer(pongTimeout)

	writeControl := func(messageType int, data []byte) error {
		c.writeMutex.Lock()
		defer c.writeMutex.Unlock()
		return conn.WriteControl(messageType, data, time.Now().Add(time.Hour))
	}

	for {
		select {
		case <-timer.C:
			writeControl(websocket.CloseNoStatusReceived, []byte("No pong received"))
			conn.Close()
			return
		case <-ticker.C:
			if err := writeControl(websocket.PingMessage, []byte(pingMessage)); err != nil {
				ticker.Stop()
				return
			}
		case <-c.PongChannel:
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(pongTimeout)
		}
	}
}

func (c *RenderingClient) ReadHandler() {
	closeChan := make(chan bool)

	defer func() {
		c.WaitGroup.Done()
		closeChan <- true
	}()

	var message messages.WebSocketMessage
	var renderReqMsg messages.RenderRequestMessage
	for {
		_, msgStr, err := c.Conn.ReadMessage()
		if err != nil {
			log.Printf("Unable to understand message: %s", err)
			break
		}

		json.Unmarshal(msgStr, &message)

		if message.Type == messages.RenderRequest {
			json.Unmarshal(msgStr, &renderReqMsg)
			operationId := renderReqMsg.OperationId
			if strings.TrimSpace(operationId) == "" {
				byteOpId, _ := uuid.NewRandom()
				operationId = byteOpId.String()
			}
			c.OperationId = operationId

			responseMessage := messages.RenderRequestResponseMessage{
				Type:        messages.RenderRequestResponse,
				OperationId: c.OperationId,
			}

			if !c.IsTracingInProgress {
				go tracer.GoTrace(&renderReqMsg.Data, true, c.Results, true, closeChan)
				c.IsTracingInProgress = true
				responseMessage.Code = messages.RenderRequestAccepted
			} else {
				responseMessage.Code = messages.RenderRequestTracingAlreadyInProgress
			}

			// Send the rendered response, not the echoed request: the master
			// expects the RenderResponse acknowledgement before pixel frames.
			c.writeJSON(responseMessage)
		}
	}
}

func (c *RenderingClient) ResultSender() {
	defer c.WaitGroup.Done()

	for pixel := range c.Results {
		if pixel == nil {
			// Terminal message carries no pixel payload; reusing the previous
			// message struct would otherwise resend its last pixel here.
			c.writeJSON(messages.WebSocketMessage{
				Type:        messages.RenderingCompleted,
				OperationId: c.OperationId,
			})
			break
		}

		// Fresh struct per pixel so a later send can never mutate or leak the
		// payload of a frame still being written to the websocket.
		c.writeJSON(messages.WebSocketMessage{
			Type:        messages.PixelResult,
			Data:        pixel,
			OperationId: c.OperationId,
		})
	}
}

func agentHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received a connection request")
	shouldConnect := true

	mutex.Lock()
	if activeConn != nil {
		log.Print("An active connection already exists")
		http.Error(w, "An active connection already exists", http.StatusConflict)
		shouldConnect = false
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		shouldConnect = false
	}
	activeConn = conn
	mutex.Unlock()

	defer func() {
		activeConn = nil
		conn.Close()
	}()

	if !shouldConnect {
		return
	}

	client := RenderingClient{
		Conn:                conn,
		PongChannel:         make(chan bool),
		Results:             make(chan *models.Pixel, 100),
		IsTracingInProgress: false,
		OperationId:         "",
	}

	client.Initialize()
}

func agentStatusHandler(w http.ResponseWriter, r *http.Request) {
	status := messages.AgentStatus{
		Available: activeConn == nil,
		Cores:     runtime.NumCPU(),
	}

	jsonStatus, err := json.Marshal(status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonStatus)
}

func main() {
	flag.Parse()

	if *addr == "" {
		flag.Usage()
		log.Fatalf("Agent address \"%s\" is invalid.", *addr)
	}

	log.SetFlags(0)
	http.HandleFunc("/", agentHandler)
	http.HandleFunc("/status", agentStatusHandler)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
