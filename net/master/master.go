package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/ioutil"
	"log"
	"net"
	"sync"
	"time"

	"net/http"

	"math"

	"github.com/DheerendraRathor/GoTracer/models"
	"github.com/DheerendraRathor/GoTracer/net/constants"
	"github.com/DheerendraRathor/GoTracer/utils"
	"github.com/gorilla/websocket"
	"gopkg.in/cheggaaa/pb.v1"
)

const (
	pongTimeout = 60 * time.Second
)

var renderSpecFile string
var agentsFile string

func init() {
	flag.StringVar(&renderSpecFile, "spec", "sample_world.json", "Name of JSON file containing rendering spec")
	flag.StringVar(&agentsFile, "agents", "agents.json", "Name of JSON file containing list of agents")
}

type Agent struct {
	URL           string
	Cores         int
	Conn          *websocket.Conn
	Env           models.Specification
	ResultChannel chan<- models.Pixel
	Completed     chan<- *Agent
	WorkDone      bool
}

func (a *Agent) Initialize() {
	workDone := false

	defer func() {
		a.WorkDone = workDone
		a.Completed <- a
		a.Conn.Close()
	}()

	a.Conn.SetPingHandler(
		func(appData string) error {
			err := a.Conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(pongTimeout))
			if err == websocket.ErrCloseSent {
				return nil
			} else if e, ok := err.(net.Error); ok && e.Temporary() {
				return nil
			}
			return err
		},
	)

	renderRequest := messages.RenderRequestMessage{
		Type: messages.RenderRequest,
		Data: a.Env,
	}

	err := a.Conn.WriteJSON(renderRequest)
	if err != nil {
		log.Printf("Unable to send JSON message to agent. %s\n", err)
		return
	}

	for {
		_, rawMsg, err := a.Conn.ReadMessage()
		if err != nil {
			fmt.Println("Error in reading message:", err)
			return
		}

		// Fresh structs per frame: WebSocketMessage.Data is an interface{}, so
		// reusing one across frames would retain the previous frame's pixel
		// when a terminal message without a Data field arrives.
		var message messages.WebSocketMessage
		if uerr := json.Unmarshal(rawMsg, &message); uerr != nil {
			fmt.Printf("Unable to parse message from agent %s: %s\n", a.URL, uerr)
			continue
		}

		switch message.Type {
		case messages.PixelResult:
			var pixelMessage messages.PixelResultMessage
			if uerr := json.Unmarshal(rawMsg, &pixelMessage); uerr != nil {
				fmt.Printf("Unable to parse pixel from agent %s: %s\n", a.URL, uerr)
				continue
			}
			a.ResultChannel <- pixelMessage.Data
		case messages.RenderingCompleted:
			workDone = true
			return
		}
	}
}

func main() {

	flag.Parse()

	specFile, e := ioutil.ReadFile(renderSpecFile)
	if e != nil {
		panic(fmt.Sprintf("Render Spec File error: %v\n", e))
	}

	agentsFile, e := ioutil.ReadFile(agentsFile)
	if e != nil {
		panic(fmt.Sprintf("Agents list file error: %v\n", e))
	}

	agents := make([]string, 0)
	json.Unmarshal(agentsFile, &agents)

	var env models.Specification
	json.Unmarshal(specFile, &env)

	pngImage := image.NewRGBA(image.Rectangle{
		image.Point{0, 0},
		image.Point{env.Image.Width, env.Image.Height},
	})

	connectedAgents := []*Agent{}

	myClient := &http.Client{
		Timeout: time.Second * 60,
		// Agents are loopback/intranet nodes reached directly; a sandbox or
		// corporate HTTP proxy cannot service their /status endpoint.
		Transport: &http.Transport{Proxy: nil},
	}

	var agentStatus messages.AgentStatus

	var renderedChannel = make(chan models.Pixel, 100)
	var workDoneChannel = make(chan *Agent, len(agents))
	var availableCores = 0

	for _, agent := range agents {
		wsURL := fmt.Sprintf("ws://%s", agent)
		httpUrl := fmt.Sprintf("http://%s/status", agent)

		response, err := myClient.Get(httpUrl)
		if err != nil {
			log.Printf("Agent '%s' is unavailable. \n\nError: %s\n", agent, err.Error())
			continue
		}

		json.NewDecoder(response.Body).Decode(&agentStatus)
		response.Body.Close()

		if !agentStatus.Available {
			log.Printf("Agent '%s' is busy\n", agent)
			continue
		}

		// Dial agents directly: a corporate HTTP proxy cannot carry the
		// websocket connection to a loopback/intranet agent, so never route
		// agent traffic through ProxyFromEnvironment.
		dialer := &websocket.Dialer{Proxy: nil}

		conn, _, err := dialer.Dial(wsURL, nil)
		if err != nil {
			fmt.Printf("Unable to dial ws connection to agent %s with error %s\n", agent, err)
			continue
		}

		connectedAgent := Agent{
			Cores:         agentStatus.Cores,
			Conn:          conn,
			URL:           agent,
			ResultChannel: renderedChannel,
			Completed:     workDoneChannel,
		}

		availableCores += agentStatus.Cores

		connectedAgents = append(connectedAgents, &connectedAgent)
	}

	if len(connectedAgents) == 0 {
		log.Fatalln("Unable to connect to any agent. Please try again later")
	}

	linesPerCore := int(math.Ceil(float64(env.Image.Width) / float64(availableCores)))

	minWidth, maxWidth := 0, 0

	for _, connectedAgent := range connectedAgents {
		cores := connectedAgent.Cores
		linesToAgent := linesPerCore * cores
		agentEnv := models.Specification(env)

		// Column patches must be contiguous and non-overlapping: assigning
		// [minWidth, minWidth+linesToAgent) makes the union of agent patches
		// exactly [0, Width) with no duplicated or unrendered columns.
		maxWidth = minWidth + linesToAgent
		if maxWidth > env.Image.Width {
			maxWidth = env.Image.Width
		}

		agentEnv.Image.Patch[2] = minWidth
		agentEnv.Image.Patch[3] = maxWidth

		minWidth = maxWidth
		connectedAgent.Env = agentEnv

		go connectedAgent.Initialize()
	}

	var wg sync.WaitGroup
	wg.Add(1)

	total := env.Image.Width * env.Image.Height
	progressBar := pb.StartNew(total)
	progressBar.ShowFinalTime = true
	progressBar.ShowTimeLeft = false

	go func() {
		defer wg.Done()
		var pixel models.Pixel
		agentsToWaitFor := len(connectedAgents)
		for {
			select {
			case pixel = <-renderedChannel:
				progressBar.Increment()
				rgbaColor := color.RGBA{pixel.Color[0], pixel.Color[1], pixel.Color[2], 255}
				pngImage.Set(pixel.I, pixel.J, rgbaColor)
			case agent := <-workDoneChannel:
				agentsToWaitFor -= 1
				log.Printf("Agent '%s' finished. Status: %t", agent.URL, agent.WorkDone)
				if agentsToWaitFor == 0 {
					// Drain pixels still buffered in the channel: the last
					// completion signal can arrive before every queued pixel
					// has been drawn, and returning now would leave holes.
					for {
						select {
						case px := <-renderedChannel:
							progressBar.Increment()
							c := color.RGBA{px.Color[0], px.Color[1], px.Color[2], 255}
							pngImage.Set(px.I, px.J, c)
						default:
							return
						}
					}
				}
			}
		}
	}()

	wg.Wait()

	pngFile := utils.CreateNestedFile(env.Image.OutputFile)
	defer pngFile.Close()

	png.Encode(pngFile, pngImage)
}
