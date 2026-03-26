package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/genai"
	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"nozomi/internal/config"
	"nozomi/internal/logger"
)

type SearchQuota struct {
	Month string `json:"month"`
	Count int    `json:"count"`
}

var (
	ctx               context.Context = context.Background()
	client            *mautrix.Client
	gclient           *genai.Client
	botConfig         config.BotConfig
	cryptoHelper      *cryptohelper.CryptoHelper
	syncer            *mautrix.DefaultSyncer
	chatMemory        sync.Map = sync.Map{}
	roomLocks         sync.Map = sync.Map{}
	bootTimeUnixmilli int64
	workdir           string
	searchMutex       sync.Mutex
	quota             SearchQuota
)

func IsExist(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func createDirOrFile() {
	logsPath := filepath.Join(workdir, "logs")
	ok, _ := IsExist(logsPath)
	if !ok {
		err := os.Mkdir(logsPath, 0777)
		if err != nil {
			log.Println("Failed to creating logs path, system down.")
			os.Exit(1)
		}
		log.Println("Create log path sucessfully.")
	}
	configPath := filepath.Join(workdir, "config.yaml")
	ok, _ = IsExist(configPath)
	if !ok {
		log.Println("config.yaml does not exist, creating...")
		data := config.BotConfig{}
		yamlData, err := yaml.Marshal(&data)
		if err != nil {
			log.Println("Marshal default config.yaml failed." + err.Error())
			os.Exit(1)
		}
		err = os.WriteFile(configPath, yamlData, 0777)
		if err != nil {
			log.Println("Failed to write default data into config.yaml. system down.")
			os.Exit(1)
		}
		log.Println("Default config.yaml has been sucessfully created.Pls complete it and run bot again.")
		os.Exit(1)
	}
	soulPath := filepath.Join(workdir, "soul.md")
	ok, _ = IsExist(soulPath)
	if !ok {
		log.Println("soul.md does not exist, creating...")
		_, err := os.Create(soulPath)
		if err != nil {
			log.Println("Auto creating soul.md failed: " + err.Error())
			os.Exit(1)
		}
		log.Println("Create soul.md sucessfully.")
	}
	databasePath := filepath.Join(workdir, "database")
	ok, _ = IsExist(databasePath)
	if !ok {
		log.Println("databasePath does not exist, auto creating at " + databasePath)
		err := os.Mkdir(databasePath, 0777)
		if err != nil {
			log.Println("Auto creating databasePath failed: " + err.Error())
			os.Exit(1)
		}
		log.Println("Create database path sucessfully.")
	}
	quotaPath := filepath.Join(workdir, "search_quota.json")
	ok, _ = IsExist(quotaPath)
	if !ok {
		log.Println("search_quota.json does not exist, creating...")
		defaultQuota := SearchQuota{
			Month: "1970-01",
			Count: 0,
		}
		quotaBytes, _ := json.MarshalIndent(defaultQuota, "", "\t")
		err := os.WriteFile(quotaPath, quotaBytes, 0644)
		if err != nil {
			log.Println("Auto creating search_quota.json failed: " + err.Error())
			os.Exit(1)
		}
		log.Println("Create search_quota.json sucessfully.")
		log.Println("All config file has created.Pls check no file is empty.")
		os.Exit(0)
	}
}

func sendToLogRoom(info string) {
	joinedRoomsResp, err := client.JoinedRooms(ctx)
	if err != nil {
		_ = logger.Log("error", "Failed to get room list when send text to log rooms. "+err.Error(), logger.Options{})
		return
	}

	joinedMap := make(map[id.RoomID]bool)
	for _, roomID := range joinedRoomsResp.JoinedRooms {
		joinedMap[roomID] = true
	}

	for _, room := range botConfig.Client.LogRoom {
		targetRoom := id.RoomID(room)
		if !joinedMap[targetRoom] {
			str := "Sending text to log room fail.No such room " + string(room)
			_ = logger.Log("info", str, logger.Options{})
			continue
		}
		if _, err := client.SendText(ctx, targetRoom, info); err != nil {
			_ = logger.Log("error", "Sending text to log room fail.", logger.Options{})
		}
	}
}

func Start() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("Failed to get executable path. system down.")
		os.Exit(1)
	}
	workdir = filepath.Dir(exePath)

	logger.Init(workdir)
	createDirOrFile()

	configPath := filepath.Join(workdir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		_ = logger.Log("error", "Error occurred when reading bot config: "+err.Error(), logger.Options{})
		os.Exit(1)
	}
	err = yaml.Unmarshal(data, &botConfig)
	if err != nil {
		_ = logger.Log("error", "config.yaml can't unmarshal when initialize bot config: "+err.Error(), logger.Options{})
		os.Exit(1)
	}

	soulPath := filepath.Join(workdir, "soul.md")
	data, err = os.ReadFile(soulPath)
	botConfig.Model.Soul = string(data)

	homeserver := botConfig.Client.HomeserverURL
	userID := botConfig.Client.UserID
	accessToken := botConfig.Client.AccessToken
	deviceID := botConfig.Client.DeviceID
	client, err = mautrix.NewClient(homeserver, userID, accessToken)
	if err != nil {
		logger.Log("error", "Couldn't initialize bot client: "+err.Error(), logger.Options{})
		os.Exit(1)
	}
	client.DeviceID = id.DeviceID(deviceID)

	gclient, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  botConfig.Model.API_KEY,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		logger.Log("error", "Failed to initialize gemini client: "+err.Error(), logger.Options{})
		os.Exit(1)
	}

	botConfig.Model.Config = &genai.GenerateContentConfig{
		MaxOutputTokens:   botConfig.Model.MaxOutputToken,
		SystemInstruction: genai.Text(botConfig.Model.Soul)[0],
	}
	if botConfig.Model.UseInternet {
		botConfig.Model.Config.Tools = []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}
	}
	if !botConfig.Model.SecureCheck {
		botConfig.Model.Config.SafetySettings = []*genai.SafetySetting{
			{
				Category:  genai.HarmCategoryHarassment,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
			{
				Category:  genai.HarmCategoryHateSpeech,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
			{
				Category:  genai.HarmCategorySexuallyExplicit,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
			{
				Category:  genai.HarmCategoryDangerousContent,
				Threshold: genai.HarmBlockThresholdBlockNone,
			},
		}
	}

	databasePath := filepath.Join(workdir, "database", "bot_crypto.db")
	cryptoHelper, err = cryptohelper.NewCryptoHelper(client, []byte("625890"), databasePath)
	if err != nil {
		_ = logger.Log("error", "Failed to initialize crypto module: "+err.Error(), logger.Options{})
		os.Exit(1)
	}
	err = cryptoHelper.Init(ctx)
	if err != nil {
		_ = logger.Log("error", "Failed to initialize crypto engine:  "+err.Error(), logger.Options{})
		os.Exit(1)
	}
	client.Crypto = cryptoHelper

	bootTimeUnixmilli = time.Now().UnixMilli()

	// Init search_quota.json
	path := filepath.Join(workdir, "search_quota.json")
	quotaData, err := os.ReadFile(path)
	if err != nil {
		str := "Failed to read search_quota.json." + err.Error()
		logger.Log("error", str, logger.Options{})
		os.Exit(1)
	}
	err = json.Unmarshal(quotaData, &quota)
	if err != nil {
		str := "Failed to unmarshal search_quota.json data." + err.Error()
		logger.Log("error", str, logger.Options{})
		os.Exit(1)
	}

	syncer = client.Syncer.(*mautrix.DefaultSyncer)
	defer cryptoHelper.Close()

	syncer.OnEventType(event.StateMember, AutoAcceptInvite)
	syncer.OnEventType(event.EventEncrypted, func(ctx context.Context, evt *event.Event) {
		if evt.Timestamp < bootTimeUnixmilli {
			return
		}
	})
	syncer.OnEventType(event.EventMessage, evtMsg)

	go startRoomCleanupTask(client)

	log.Printf("Robot sucessfully initialize! %s now is runing!", client.UserID.String())
	_ = logger.Log("info", "Robot init sucessfully.", logger.Options{})

	err = client.Sync()
	if err != nil {
		panic(err)
	}
}
