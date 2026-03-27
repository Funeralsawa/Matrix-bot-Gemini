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

type TimeLog struct {
	Time time.Time `json:"Time"`
}

var (
	ctx               context.Context = context.Background()
	timeLog           TimeLog
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
	GlobalTokenUsage  ConsumeToken
	tokenMutex        sync.Mutex // 专门保护账单并发写入的锁
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
	dataPath := filepath.Join(workdir, "data")
	ok, _ = IsExist(dataPath)
	if !ok {
		log.Println("dataPath does not exist, auto creating at " + dataPath)
		err := os.Mkdir(dataPath, 0777)
		if err != nil {
			log.Println("Auto creating dataPath failed: " + err.Error())
			os.Exit(1)
		}
		log.Println("Create data path sucessfully.")
	}
	quotaPath := filepath.Join(workdir, "data", "search_quota.json")
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
	}
	tokenUsagePath := filepath.Join(workdir, "data", "token_usage.json")
	ok, _ = IsExist(tokenUsagePath)
	if !ok {
		log.Println("token_usage.json does not exist, creating...")
		defaultUsage := ConsumeToken{}
		bytes, _ := json.MarshalIndent(defaultUsage, "", "\t")
		err := os.WriteFile(tokenUsagePath, bytes, 0644)
		if err != nil {
			log.Println("Auto creating token_usage.json failed: " + err.Error())
			os.Exit(1)
		}
		log.Println("Create token_usage.json sucessfully.")
	}
	timePath := filepath.Join(workdir, "data", "time.json")
	ok, _ = IsExist(timePath)
	if !ok {
		log.Println("time.json does not exist, creating...")
		defaultTime := TimeLog{Time: time.Now()}
		bytes, _ := json.MarshalIndent(defaultTime, "", "\t")
		err := os.WriteFile(timePath, bytes, 0644)
		if err != nil {
			log.Println("Auto creating time.json failed: " + err.Error())
			os.Exit(1)
		}
		log.Println("Create time.json sucessfully.")
		log.Println("All config file has created.Pls check no file is empty.")
		os.Exit(0)
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
	password := botConfig.Model.DatabasePassword
	cryptoHelper, err = cryptohelper.NewCryptoHelper(client, []byte(password), databasePath)
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

	// Init time.json
	path := filepath.Join(workdir, "data", "time.json")
	timeData, err := os.ReadFile(path)
	if err != nil {
		str := "Failed to read time.json." + err.Error()
		logger.Log("error", str, logger.Options{})
		os.Exit(1)
	}
	err = json.Unmarshal(timeData, &timeLog)
	if err != nil {
		str := "Failed to unmarshal time.json data." + err.Error()
		logger.Log("error", str, logger.Options{})
		os.Exit(1)
	}

	// Init search_quota.json
	path = filepath.Join(workdir, "data", "search_quota.json")
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

	// Init token_usage.json
	LoadTokenUsage()

	syncer = client.Syncer.(*mautrix.DefaultSyncer)

	defer cryptoHelper.Close()

	syncer.OnEventType(event.StateMember, EvtMember)
	syncer.OnEventType(event.EventEncrypted, func(ctx context.Context, evt *event.Event) {
		if evt.Timestamp < bootTimeUnixmilli {
			return
		}
	})
	syncer.OnEventType(event.EventMessage, evtMsg)

	go startRoomCleanupTask()    //GC_1
	go clearNonExistRoomMemory() //GC_2
	go startBillingCheckTask()

	log.Printf("Robot sucessfully initialize! %s now is runing!", client.UserID.String())
	_ = logger.Log("info", "Robot init sucessfully.", logger.Options{})

	err = client.Sync()
	if err != nil {
		panic(err)
	}
}
