//go:build js && wasm

// The offline player uses the exact same Go narrative engine as the server.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/Azhe-ZJ/Playfielder/internal/story"
)

func main() {
	fn := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		if len(args) != 1 {
			return `{"error":"invalid_arguments"}`
		}
		var in struct {
			Flow   model.Story  `json:"flow"`
			State  story.State  `json:"state"`
			Action story.Action `json:"action"`
			Now    int64        `json:"now"`
		}
		if err := json.Unmarshal([]byte(args[0].String()), &in); err != nil {
			return `{"error":"invalid_json"}`
		}
		out, err := story.Step(in.Flow, in.State, in.Action, in.Now)
		if err != nil {
			b, _ := json.Marshal(map[string]string{"error": err.Error()})
			return string(b)
		}
		replies := map[string][]model.Reply{}
		for _, n := range in.Flow.Nodes {
			replies[n.ID] = story.Available(in.Flow, out, n.ID)
		}
		b, _ := json.Marshal(struct {
			State   story.State              `json:"state"`
			Replies map[string][]model.Reply `json:"replies"`
		}{out, replies})
		return string(b)
	})
	js.Global().Set("playfielderStoryStep", fn)
	select {}
}
