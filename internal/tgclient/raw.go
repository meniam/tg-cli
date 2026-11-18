package tgclient

import (
	"encoding/json"
	"log"

	"github.com/gotd/td/tdp"
	"tg-cli/internal/tgraw"
)

func messageRaw(object tdp.Object) json.RawMessage {
	raw, err := tgraw.Marshal(object)
	if err != nil {
		log.Printf("Telegram object serialization failed: %T: %v", object, err)
		return nil
	}
	return raw
}
