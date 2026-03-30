package bot

import (
	"nozomi/internal/logger"
	"time"

	"maunium.net/go/mautrix/id"
)

func checkAndRestoreProfile() {
	// 1. 检查并恢复昵称
	if botConfig.Client.DisplayName != "" {
		respName, err := client.GetDisplayName(ctx, client.UserID)
		if err != nil {
			_ = logger.Log("error", "Failed to get bot display name: "+err.Error(), logger.Options{})
		} else if respName == nil || respName.DisplayName != botConfig.Client.DisplayName {
			setErr := client.SetDisplayName(ctx, botConfig.Client.DisplayName)
			if setErr != nil {
				_ = logger.Log("error", "Failed to restore display name: "+setErr.Error(), logger.Options{})
			} else {
				_ = logger.Log("info", "Auto restored bot display name to: "+botConfig.Client.DisplayName, logger.Options{})
			}
		}
	}

	// 2. 检查并恢复头像
	if botConfig.Client.AvatarURL != "" {
		parsedURI, parseErr := id.ParseContentURI(botConfig.Client.AvatarURL)
		if parseErr != nil {
			_ = logger.Log("error", "Failed to parse bot avatar url: "+parseErr.Error(), logger.Options{})
			return
		}
		respAvatar, err := client.GetAvatarURL(ctx, client.UserID)
		if err != nil {
			_ = logger.Log("error", "Failed to get bot avatar url: "+err.Error(), logger.Options{})
		} else if respAvatar.String() != botConfig.Client.AvatarURL {
			setErr := client.SetAvatarURL(ctx, parsedURI)
			if setErr != nil {
				_ = logger.Log("error", "Failed to restore avatar: "+setErr.Error(), logger.Options{})
			} else {
				_ = logger.Log("info", "Auto restored bot avatar.", logger.Options{})
			}
		}
	}
}

func startProfileKeeperTask() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// 每次程序刚启动时，强制执行一次校验
	checkAndRestoreProfile()

	for {
		<-ticker.C
		checkAndRestoreProfile()
	}
}
