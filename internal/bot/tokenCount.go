package bot

import (
	"encoding/json"
	"fmt"
	"nozomi/internal/logger"
	"os"
	"path/filepath"
	"time"
)

type Token struct {
	Input  int64 `json:"Input"`
	Output int64 `json:"Output"`
	Think  int64 `json:"Think"`
}

func (t *Token) Add(input, output, think int32) {
	t.Input += int64(input)
	t.Output += int64(output)
	t.Think += int64(think)
}

func (t *Token) CountTotal() int64 {
	return t.Input + t.Output + t.Think
}

func (t *Token) ResetTokenUsage() {
	t.Input = 0
	t.Output = 0
	t.Think = 0
}

type ConsumeToken struct {
	Day   Token `json:"Day"`
	Month Token `json:"Month"`
	Year  Token `json:"Year"`
}

func (c *ConsumeToken) Record(input, output, think int32) {
	c.Day.Add(input, output, think)
	c.Month.Add(input, output, think)
	c.Year.Add(input, output, think)
}

func SaveTimeLog() {
	data, err := json.Marshal(timeLog)
	if err != nil {
		str := "Marshal timeLog data failed." + err.Error()
		_ = logger.Log("error", str, logger.Options{})
		sendToLogRoom(str)
		return
	}
	path := filepath.Join(workdir, "data", "time.json")
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		str := "Fail to write time data into time.json." + err.Error()
		_ = logger.Log("error", str, logger.Options{})
		sendToLogRoom(str)
		return
	}
}

func CheckAndResetBilling() {
	now := time.Now()

	// 如果时间是空的，直接定为现在，不触发空账单
	if timeLog.Time.IsZero() {
		timeLog.Time = now
		return
	}

	// 使用绝对格式进行安全比对，杜绝“同日跨月” Bug
	isNewDay := now.Format("2006-01-02") != timeLog.Time.Format("2006-01-02")
	isNewMonth := now.Format("2006-01") != timeLog.Time.Format("2006-01")
	isNewYear := now.Format("2006") != timeLog.Time.Format("2006")

	// 如果连天都没变，直接安全退出
	if !isNewDay {
		return
	}

	// 1. 发送并清零日账单
	if isNewDay {
		str := fmt.Sprintf("Token 日账单：\n\tInput: %d\n\tOutput: %d\n\tThink: %d\n\tTotal: %d",
			GlobalTokenUsage.Day.Input, GlobalTokenUsage.Day.Output, GlobalTokenUsage.Day.Think, GlobalTokenUsage.Day.CountTotal())
		sendToLogRoom(str)
		GlobalTokenUsage.Day.ResetTokenUsage()
	}

	// 2. 发送并清零月账单
	if isNewMonth {
		str := fmt.Sprintf("Token 月账单：\n\tInput: %d\n\tOutput: %d\n\tThink: %d\n\tTotal: %d",
			GlobalTokenUsage.Month.Input, GlobalTokenUsage.Month.Output, GlobalTokenUsage.Month.Think, GlobalTokenUsage.Month.CountTotal())
		sendToLogRoom(str)
		GlobalTokenUsage.Month.ResetTokenUsage()
	}

	// 3. 发送并清零年账单
	if isNewYear {
		str := fmt.Sprintf("Token 年账单：\n\tInput: %d\n\tOutput: %d\n\tThink: %d\n\tTotal: %d",
			GlobalTokenUsage.Year.Input, GlobalTokenUsage.Year.Output, GlobalTokenUsage.Year.Think, GlobalTokenUsage.Year.CountTotal())
		sendToLogRoom(str)
		GlobalTokenUsage.Year.ResetTokenUsage()
	}

	// 更新游标时间，防止无限重置
	timeLog.Time = now

	SaveTimeLog()
}

// LoadTokenUsage 在机器人启动时调用
func LoadTokenUsage() {
	path := filepath.Join(workdir, "data", "token_usage.json")
	data, err := os.ReadFile(path)
	if err != nil {
		str := "Failed to read token_usage.json." + err.Error()
		_ = logger.Log("error", str, logger.Options{})
		return
	}
	err = json.Unmarshal(data, &GlobalTokenUsage)
	if err != nil {
		_ = logger.Log("error", "Failed to parse token_usage.json: "+err.Error(), logger.Options{})
	}
}

func SaveTokenUsage() {
	path := filepath.Join(workdir, "data", "token_usage.json")
	data, err := json.MarshalIndent(GlobalTokenUsage, "", "\t")
	if err != nil {
		_ = logger.Log("error", "Failed to marshal token usage: "+err.Error(), logger.Options{})
		return
	}

	err = os.WriteFile(path, data, 0644)
	if err != nil {
		str := "Saving data to token_usage.json fail: " + err.Error()
		_ = logger.Log("error", str, logger.Options{})
		sendToLogRoom(str)
	}
}

func startBillingCheckTask() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		<-ticker.C

		tokenMutex.Lock()
		CheckAndResetBilling()
		tokenMutex.Unlock()
	}
}
