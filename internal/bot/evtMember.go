package bot

import (
	"context"
	"fmt"

	"nozomi/internal/logger"

	"maunium.net/go/mautrix/event"
)

func EvtMember(ctx context.Context, evt *event.Event) {
	memberEvent := evt.Content.AsMember()
	// 如果不是成员事件，或者事件的主体不是机器人自己，直接忽略
	if memberEvent == nil || evt.StateKey == nil || *evt.StateKey != client.UserID.String() {
		return
	}

	switch memberEvent.Membership {
	case event.MembershipInvite:
		//自动同意房间邀请
		joinedRoomsResp, err := client.JoinedRooms(ctx)
		if err != nil {
			_ = logger.Log("error", "Failed to get the joined room list."+err.Error(), logger.Options{})
			return
		}
		for _, roomID := range joinedRoomsResp.JoinedRooms {
			if roomID == evt.RoomID {
				return
			}
		}
		_ = logger.Log("info", fmt.Sprintf("Receive a room Invite from %s with room ID: %s", evt.Sender, evt.RoomID), logger.Options{})
		_, err = client.JoinRoomByID(ctx, evt.RoomID)
		if err != nil {
			str := "Failed to join room with ID: " + evt.RoomID.String()
			_ = logger.Log("error", str, logger.Options{})
		} else {
			_ = logger.Log("info", fmt.Sprintf("Join room(%s) sucessfully.", evt.RoomID), logger.Options{})
			_, _ = client.SendText(ctx, evt.RoomID, "你好，我是希。")
		}
	case event.MembershipLeave, event.MembershipBan:
		// 处理退群或被踢
		roomIDStr := evt.RoomID.String()
		chatMemory.Delete(roomIDStr)
		str := fmt.Sprintf("Bot left or was kicked from room %s. Memory was cleared.", roomIDStr)
		_ = logger.Log("info", str, logger.Options{})
	}
}
