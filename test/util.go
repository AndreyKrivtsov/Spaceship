package test

import (
	"bytes"
	"context"
	"github.com/golang/protobuf/jsonpb"
	"github.com/gorilla/websocket"
	"github.com/jinzhu/configor"
	"github.com/mediocregopher/radix/v3"
	"github.com/satori/go.uuid"
	"google.golang.org/grpc"
	"net"
	"runtime"
	"spaceship/api"
	"spaceship/apigrpc"
	"spaceship/server"
	"spaceship/socketapi"
	"testing"
)

var (
	jsonpbMarshaler = &jsonpb.Marshaler{
		EnumsAsInts:  true,
		EmitDefaults: false,
		Indent:       "",
		OrigName:     true,
	}
	jsonpbUnmarshaler = &jsonpb.Unmarshaler{
		AllowUnknownFields: false,
	}
)

func NewServer(t *testing.T) (*server.Server) {

	appContext, _ := context.WithCancel(context.Background())

	config := &server.Config{}
	err := configor.Load(config, "config.yml")
	if err != nil {
		t.Error("Error while reading configurations from config.yml")
	}
	redis := redisConnect(t, config)

	logger := server.NewLogger(config)
	defer logger.Sync()

	db := server.ConnectDB(config, logger)
	notification := server.NewNotificationService(db, config, logger)
	leaderboard := server.NewLeaderboard(db, logger)
	stats := server.NewStatsHolder(logger)
	sessionHolder := server.NewSessionHolder(config)
	gameHolder := server.NewGameHolder(redis, jsonpbMarshaler, jsonpbUnmarshaler, leaderboard, notification)
	pubsub := server.NewPubSub(config, sessionHolder, jsonpbMarshaler, jsonpbUnmarshaler, logger, appContext)
	matchmaker := server.NewLocalMatchMaker(redis, gameHolder, sessionHolder, notification, logger, config, pubsub, appContext)
	pipeline := server.NewPipeline(config, jsonpbMarshaler, jsonpbUnmarshaler, gameHolder, sessionHolder, matchmaker, db, redis, notification, logger, pubsub)
	matchmaker.SetPipeline(pipeline)

	gameHolder.Add(&PTGame{})
	gameHolder.Add(&ATGame{})
	gameHolder.Add(&RTGame{})

	sessionHolder.SetLeaveListener(matchmaker.LeaveActiveGames)

	return server.StartServer(sessionHolder, gameHolder, config, jsonpbMarshaler, jsonpbUnmarshaler, pipeline, db, leaderboard, stats, logger)

}

func CreateSession(t *testing.T) (*api.Session) {

	conn, err := grpc.Dial("localhost:7349", grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}

	client := apigrpc.NewSpaceShipClient(conn)
	session, err := client.AuthenticateFingerprint(context.Background(), &api.AuthenticateFingerprint{
		Fingerprint: generateUUID(),
	})
	if err != nil {
		t.Fatal(err)
	}

	return session

}

func CreateSessionChan(failChan chan string) (*api.Session) {

	conn, err := grpc.Dial("localhost:7349", grpc.WithInsecure())
	if err != nil {
		failChan <- err.Error()
	}

	client := apigrpc.NewSpaceShipClient(conn)
	session, err := client.AuthenticateFingerprint(context.Background(), &api.AuthenticateFingerprint{
		Fingerprint: generateUUID(),
	})
	if err != nil {
		failChan <- err.Error()
	}

	return session

}

func CreateSocketConn(t *testing.T, token string) (*websocket.Conn, chan []byte) {

	c, _, err := websocket.DefaultDialer.Dial("ws://localhost:7350/ws?token=" + token, nil)
	if err != nil {
		t.Fatal(err)
	}

	onMessageChan := make(chan []byte)

	go func() {
		defer close(onMessageChan)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {

				}else if e, ok := err.(*net.OpError); ok || e.Err.Error() == "use of closed network connection" {

				}else{
					t.Fatal(err)
				}
				//Even if connection was closed or error occured we should break the loop
				break
			}
			onMessageChan <- message
		}
	}()

	return c, onMessageChan

}

func CreateSocketConnChan(failChan chan string, token string) (*websocket.Conn, chan []byte) {

	c, _, err := websocket.DefaultDialer.Dial("ws://localhost:7350/ws?token=" + token, nil)
	if err != nil {
		failChan <- err.Error()
	}

	onMessageChan := make(chan []byte)

	go func() {
		defer close(onMessageChan)
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {

				}else if e, ok := err.(*net.OpError); ok || e.Err.Error() == "use of closed network connection" {

				}else{
					failChan <- err.Error()
				}
				//Even if connection was closed or error occured we should break the loop
				break
			}
			onMessageChan <- message
		}
	}()

	return c, onMessageChan

}

func WriteMessage(failChan chan string, client *websocket.Conn, envelope *socketapi.Envelope) {
	var payload []byte
	var err error
	var buf bytes.Buffer

	if err = jsonpbMarshaler.Marshal(&buf, envelope); err == nil {
		payload = buf.Bytes()
	}
	if err != nil {
		failChan <- "Could not marshal envelope " + err.Error()
		runtime.Goexit()
	}

	err = client.WriteMessage(websocket.TextMessage, payload)
	if err != nil {
		failChan <- err.Error()
		runtime.Goexit()
	}
}

func ReadMessage(failChan chan string, onMessageChan chan []byte) (socketapi.Envelope) {
	var payload []byte
	var env socketapi.Envelope

	payload = <- onMessageChan

	if err := jsonpbUnmarshaler.Unmarshal(bytes.NewReader(payload), &env); err != nil {
		failChan <- err.Error()
		runtime.Goexit()
	}

	return env
}

func redisConnect(t *testing.T, config *server.Config) radix.Client{
	var redisClient radix.Client
	var err error

	if config.RedisConfig.CluesterEnabled {
		redisClient, err = radix.NewCluster([]string{config.RedisConfig.ConnString})
		if err != nil {
			t.Fatal("Redis Connection Failed", err)
		}
	}else{
		redisClient, err = radix.NewPool("tcp", config.RedisConfig.ConnString, 1)
		if err != nil {
			t.Fatal("Redis Connection Failed", err)
		}
	}
	return redisClient
}

func generateUUID() string {
	return uuid.NewV4().String()
}