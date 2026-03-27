package bot

import (
	"context"
	"fmt"
	"time"

	"nozomi/internal/logger"
)

// 自动处理空房间
func startRoomCleanupTask() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		<-ticker.C
		ctx := context.Background()

		joinedRoomsResp, err := client.JoinedRooms(ctx)
		if err != nil {
			_ = logger.Log("error", "Failed to get room list when begin to cleanup rooms."+err.Error(), logger.Options{})
			continue
		}

		for _, roomID := range joinedRoomsResp.JoinedRooms {
			membersResp, err := client.JoinedMembers(ctx, roomID)
			if err != nil {
				str := fmt.Sprintf("Failed to get member of the room %s with error %s", roomID, err.Error())
				_ = logger.Log("error", str, logger.Options{})
				continue
			}

			if len(membersResp.Joined) <= 1 {
				_, err := client.LeaveRoom(ctx, roomID)
				if err != nil {
					str := fmt.Sprintf("Failed to exit the empty room %s with error %s", roomID.String(), err.Error())
					_ = logger.Log("error", str, logger.Options{})
					sendToLogRoom(str)
				} else {
					str := fmt.Sprintf("Exit the empty room %s sucessfully.", roomID.String())
					str2 := fmt.Sprintf("The chat memory of room %s was deleted.", roomID.String())
					_ = logger.Log("info", str, logger.Options{})

					chatMemory.Delete(roomID.String())
					if _, ok := chatMemory.Load(roomID); !ok {
						_ = logger.Log("info", str2, logger.Options{})
					} else {
						str := fmt.Sprintf("Canot delete chat memory of room %s", roomID.String())
						_ = logger.Log("error", str, logger.Options{})
						sendToLogRoom(str)
					}
				}
			}
		}
		_ = logger.Log("info", "The empty room cleanup task has done.", logger.Options{})
	}
}
