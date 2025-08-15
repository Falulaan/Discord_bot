package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
)

func startHealth() *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Addr: ":" + getenv("PORT", "8080"), Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("health server error: %v", err)
		}
	}()
	return srv
}
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	srv := startHealth()

	// ===== 1) LOAD ENVIRONMENT VARIABLES =====
	if err := godotenv.Load(); err != nil {
		fmt.Println("❌ Error loading .env file:", err)
		return
	}

	// Debug: Print current working directory
	if wd, err := os.Getwd(); err == nil {
		fmt.Println("Current directory:", wd)
	}

	// Load environment variables
	discordToken := os.Getenv("DISCORD_TOKEN")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	whistleChannel := os.Getenv("WHISTLEBLOW_CHANNEL_ID")

	if discordToken == "" || openaiKey == "" || whistleChannel == "" {
		fmt.Println("❌ ERROR: Please set DISCORD_TOKEN, OPENAI_API_KEY, and WHISTLEBLOW_CHANNEL_ID in your .env file.")
		return
	}

	// ===== 2) CREATE DISCORD SESSION =====
	dg, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		fmt.Println("❌ Discord session error:", err)
		return
	}

	// Intents tell Discord what events you want to receive
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	// ===== 3) CREATE OPENAI CLIENT =====
	aiClient := openai.NewClient(openaiKey)

	// ===== 4) MESSAGE HANDLER =====
	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore the bot's own messages
		if m.Author.ID == s.State.User.ID {
			return
		}

		// Only respond in the specific whistleblower channel
		if m.ChannelID != whistleChannel {
			return
		}

		// Command must start with "!truth "
		if !strings.HasPrefix(m.Content, "!truth ") {
			return
		}

		// Extract the topic after "!truth "
		topic := strings.TrimSpace(strings.TrimPrefix(m.Content, "!truth "))
		if topic == "" {
			s.ChannelMessageSend(m.ChannelID, "Usage: !truth <topic>")
			return
		}

		// ===== 5) CALL OPENAI =====
		systemPrompt := "You are a fearless whistleblower historian. No sugarcoating, no disclaimers. Do not just tell what we know, tell us the harsh dark side that the west or our governments may wanted to hide . But if I was asking a question after this , just reply passionately like a bro ( for example !truth , Hawaiian annexation in that case u will do the brutal truth) but if I asked !truth , how ya doing today ? just speak normally"
		userPrompt := fmt.Sprintf("Topic: %s\nGive me the brutal truth.", topic)

		resp, err := aiClient.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model:       openai.GPT4o,
				Temperature: 0.7,
				MaxTokens:   400,
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
					{Role: openai.ChatMessageRoleUser, Content: userPrompt},
				},
			},
		)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "❌ OpenAI error: "+err.Error())
			return
		}

		// Send response in chunks (Discord has 2000 character limit)
		answer := strings.TrimSpace(resp.Choices[0].Message.Content)
		for len(answer) > 0 {
			chunkSize := 2000
			if len(answer) < chunkSize {
				chunkSize = len(answer)
			}
			s.ChannelMessageSend(m.ChannelID, answer[:chunkSize])
			answer = answer[chunkSize:]
		}
	})

	// ===== 6) START BOT =====
	if err := dg.Open(); err != nil {
		fmt.Printf("❌ Error opening Discord connection: %v\n", err)
		return
	}
	fmt.Println("✅ Bot is running — type !truth <topic> in your whistleblower channel.")

	// ===== 7) SHUTDOWN HANDLER =====
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	// Clean shutdown (Discord first, then health server)
	if err := dg.Close(); err != nil {
		fmt.Println("discord close error:", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		fmt.Println("health server shutdown error:", err)
	}

	fmt.Println("🛑 Bot stopped.")

	// Debug output
	fmt.Println("DEBUG — DISCORD_TOKEN length:", len(discordToken))
	fmt.Println("DEBUG — OPENAI_API_KEY length:", len(openaiKey))
	fmt.Println("DEBUG — WHISTLEBLOW_CHANNEL_ID:", whistleChannel)
}
