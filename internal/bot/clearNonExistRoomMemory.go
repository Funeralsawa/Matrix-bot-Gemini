package bot

import (
	"nozomi/internal/logger"
	"time"
)

// 自动清除无加入房间的记忆，兜底轮询GC机制
func clearNonExistRoomMemory() {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		<-ticker.C
		joinedRoomResp, err := client.JoinedRooms(ctx)
		if err != nil {
			str := "Failed to retrieve the list of joined rooms when clear non-exist room Memories: " + err.Error()
			_ = logger.Log("error", str, logger.Options{})
			continue
		}
		joinedRooms := make(map[string]bool)
		for _, roomID := range joinedRoomResp.JoinedRooms {
			joinedRooms[string(roomID)] = true
		}
		chatMemory.Range(func(key, val any) bool {
			roomID := key.(string)
			if !joinedRooms[roomID] {
				chatMemory.Delete(key)
				str := "Auto cleared memory for non-exist room: " + roomID
				_ = logger.Log("info", str, logger.Options{})
			}
			return true
		})
	}
}
