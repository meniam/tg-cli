package tgclient

import (
	"github.com/gotd/td/tg"
	"testing"
	"tg-cli/internal/rpc"
)

func TestNotifySettingsPreserveOtherFields(t *testing.T) {
	current := &tg.PeerNotifySettings{}
	current.SetSilent(true)
	current.SetShowPreviews(false)
	current.SetMuteUntil(123)
	current.SetStoriesMuted(true)
	current.SetStoriesHideSender(false)
	until := 456
	out := notifySettingsInput(current, rpc.NotifyParams{MuteUntil: &until})
	if v, ok := out.GetMuteUntil(); !ok || v != 456 {
		t.Fatal("mute not updated")
	}
	if v, ok := out.GetSilent(); !ok || !v {
		t.Fatal("silent lost")
	}
	if v, ok := out.GetShowPreviews(); !ok || v {
		t.Fatal("previews lost")
	}
	if v, ok := out.GetStoriesMuted(); !ok || !v {
		t.Fatal("stories mute lost")
	}
	if v, ok := out.GetStoriesHideSender(); !ok || v {
		t.Fatal("stories sender setting lost")
	}
}

func TestDifferentPlatformSoundsAreRejected(t *testing.T) {
	current := &tg.PeerNotifySettings{}
	current.SetIosSound(&tg.NotificationSoundNone{})
	current.SetAndroidSound(&tg.NotificationSoundDefault{})
	current.SetOtherSound(&tg.NotificationSoundDefault{})
	if samePlatformSounds(current) {
		t.Fatal("different sounds accepted")
	}
	current.SetIosSound(&tg.NotificationSoundDefault{})
	if !samePlatformSounds(current) {
		t.Fatal("equal sounds rejected")
	}
}
