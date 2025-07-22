// main.go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/bwmarrin/discordgo"
	openai "github.com/sashabaranov/go-openai"
)

func main() {
	// 1) Load env-vars
	discordToken := os.Getenv("WhistleblowBot_token")
	openaiKey := os.Getenv("discord_bot_key")
	whistleChan := os.Getenv("WHISTLEBLOW_CHANNEL_ID")
	if discordToken == "" || openaiKey == "" || whistleChan == "" {
		fmt.Println("ERROR: set WhistleblowBot_token, discord_bot_key & WHISTLEBLOW_CHANNEL_ID")
		return
	}

	// 2) Init Discord & OpenAI clients
	dg, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		fmt.Println("Discord session error:", err)
		return
	}
	oa := openai.NewClient(openaiKey)

	// 3) Register on-demand !truth handler
	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID || m.ChannelID != whistleChan {
			return
		}
		if !strings.HasPrefix(m.Content, "!truth ") {
			return
		}

		topic := strings.TrimSpace(strings.TrimPrefix(m.Content, "!truth "))
		if topic == "" {
			s.ChannelMessageSend(m.ChannelID, "Usage: `!truth <topic>`")
			return
		}

		// build system + user prompt
		sys := `You are a fearless whistleblower historian. Unfiltered—no sugarcoating or disclaimers.`
		usr := fmt.Sprintf("Topic: %s\nGive me the brutal truth.", topic)

		// call ChatGPT
		resp, err := oa.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model:       openai.GPT3Dot5Turbo,
				Temperature: 0.7,
				MaxTokens:   600,
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleSystem, Content: sys},
					{Role: openai.ChatMessageRoleUser, Content: usr},
				},
			},
		)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "❌ OpenAI error: "+err.Error())
			return
		}

		// send back, respecting Discord’s 2000-char limit
		answer := strings.TrimSpace(resp.Choices[0].Message.Content)
		for len(answer) > 0 {
			cut := len(answer)
			if cut > 2000 {
				if idx := strings.LastIndex(answer[:2000], "\n"); idx > 0 {
					cut = idx
				} else {
					cut = 2000
				}
			}
			s.ChannelMessageSend(m.ChannelID, answer[:cut])
			answer = answer[cut:]
		}
	})

	// 4) Open connection & block until CTRL+C
	if err = dg.Open(); err != nil {
		fmt.Println("Error opening Discord connection:", err)
		return
	}
	fmt.Println("Bot is running—type !truth in your channel to test.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	dg.Close()
}
