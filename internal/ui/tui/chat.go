package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/upstream"
)

type chatResponseMsg struct {
	text string
	err  error
}
type chatModel struct {
	ctx        context.Context
	client     upstream.Client
	input      textinput.Model
	log        viewport.Model
	messages   []llm.Message
	transcript []string
	busy       bool
}

func RunChat(ctx context.Context, client upstream.Client) error {
	m := New(Config{Context: ctx, Start: Chat})
	m.chat = chatModel{ctx: ctx, client: client, input: textinput.New(), log: viewport.New(viewport.WithWidth(76), viewport.WithHeight(14))}
	m.chat.input.Prompt = "You: "
	m.chat.input.CharLimit = 16000
	m.chat.input.Focus()
	_, err := tea.NewProgram(m, programOptions()...).Run()
	if err != nil {
		return err
	}
	return m.Error
}
func (c *chatModel) resize(w, h int) {
	c.log.SetWidth(max(10, w-2))
	c.log.SetHeight(max(3, h-6))
	c.input.SetWidth(max(10, w-8))
}
func (c *chatModel) update(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "enter" && !c.busy {
		text := strings.TrimSpace(c.input.Value())
		if text == "" {
			return nil
		}
		c.input.SetValue("")
		c.busy = true
		c.messages = append(c.messages, llm.Message{Role: "user", Content: text})
		c.transcript = append(c.transcript, "You: "+text)
		c.log.SetContent(strings.Join(c.transcript, "\n\n"))
		c.log.GotoBottom()
		messages := append([]llm.Message(nil), c.messages...)
		return func() tea.Msg {
			var b strings.Builder
			events, err := c.client.Stream(c.ctx, &llm.Request{Messages: messages, MaxTokens: 2048})
			if err == nil {
				for ev := range events {
					if ev.Type == llm.EventText {
						b.WriteString(ev.Text)
					}
					if ev.Type == llm.EventError {
						err = fmt.Errorf("%s", ev.Text)
					}
				}
			}
			return chatResponseMsg{b.String(), err}
		}
	}
	var cmd tea.Cmd
	if msg.String() == "pgup" || msg.String() == "pgdown" {
		c.log, cmd = c.log.Update(msg)
	} else if !c.busy {
		c.input, cmd = c.input.Update(msg)
	}
	return cmd
}
func (c *chatModel) receive(v chatResponseMsg) tea.Cmd {
	c.busy = false
	if v.err != nil {
		c.transcript = append(c.transcript, "Error: "+v.err.Error())
	} else {
		c.messages = append(c.messages, llm.Message{Role: "assistant", Content: v.text})
		c.transcript = append(c.transcript, "Model: "+v.text)
	}
	c.log.SetContent(strings.Join(c.transcript, "\n\n"))
	c.log.GotoBottom()
	return nil
}
func (c *chatModel) view() string {
	state := ""
	if c.busy {
		state = "\nWaiting for the paced reply…"
	}
	return fmt.Sprintf("triaLM chat · cloud answers at local pace\n%s\n%s%s", c.log.View(), c.input.View(), state)
}
