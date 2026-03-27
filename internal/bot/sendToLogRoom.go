package bot

import (
	"nozomi/internal/logger"

	"maunium.net/go/mautrix/id"
)

func sendToLogRoom(info string) {
	for _, room := range botConfig.Client.LogRoom {
		targetRoom := id.RoomID(room)
		if _, err := client.SendText(ctx, targetRoom, info); err != nil {
			_ = logger.Log("error", "Sending text to log room fail: "+err.Error(), logger.Options{})
		}
	}
}
