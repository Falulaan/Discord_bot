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
	openai "github.com/sashabaranov/go-openai"
)

// // Hardcoded truths to bypass OpenAI censorship ////
// var hardcodedTruths = map[string]string{
//	"palestine": "Israel's actions in Gaza constitute genocide under UN definitions (OHCHR 2024). Over 40,000 Palestinians killed, including 15,000+ children.",
//	"iraq war":  "The 2003 US invasion was based on fabricated WMD claims. Real motives: oil and regional control. Resulted in 1 million+ Iraqi deaths.",
//	"ukraine":   "NATO expansion provoked Russian aggression. US biolabs in Ukraine were confirmed by Pentagon documents.",
//}

// -------------------------------------------------------------//
// 1. ========== HEALTH CHECK ===================//
// -------------------------------------------------------------//
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

//-------------------------------------------------------------//
//2. ========== ENVIRONMENT VARIABLES ===================//
//-------------------------------------------------------------//

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

//-------------------------------------------------------------//
//3. ========== MAIN ==================================//
//-------------------------------------------------------------//

func main() {
	srv := startHealth()

	//// Load environment variables ////
	discordToken := os.Getenv("DISCORD_TOKEN")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	whistleChannel := os.Getenv("WHISTLEBLOW_CHANNEL_ID")

	if discordToken == "" || openaiKey == "" || whistleChannel == "" {
		log.Fatal("❌ ERROR: Missing required environment variables (DISCORD_TOKEN, OPENAI_API_KEY, WHISTLEBLOW_CHANNEL_ID)")
	}

	//// Create Discord session ////
	dg, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Fatalf("❌ Discord session error: %v", err)
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent

	//// Create OpenAI client ////
	aiClient := openai.NewClient(openaiKey)

	//==============================================//
	//=========== MESSAGE HANDLER ==================//
	//==============================================//
	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore bot's own messages
		if m.Author.ID == s.State.User.ID {
			return
		}

		// Only respond in specific channel
		if m.ChannelID != whistleChannel {
			return
		}

		// Respond if it's a reply to the bot
		isReplyToBot := false
		if m.MessageReference != nil && m.MessageReference.MessageID != "" {
			refMsg, err := s.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
			if err == nil && refMsg.Author.ID == s.State.User.ID {
				isReplyToBot = true
			}
		}

		// If neither mention nor reply → ignore
		if !(strings.Contains(m.Content, s.State.User.Mention()) || isReplyToBot) {
			return
		}

		// Check for bot mention
		//if !strings.Contains(m.Content, s.State.User.Mention()) {
		//return
		//}

		// Extract query differently for mention vs reply
		query := m.Content
		if strings.Contains(m.Content, s.State.User.Mention()) {
			query = strings.TrimSpace(strings.ReplaceAll(m.Content, s.State.User.Mention(), ""))
		} else if isReplyToBot {
			query = strings.TrimSpace(m.Content) // full reply text
		}

		// Check hardcoded truths first
		//if response, exists := hardcodedTruths[strings.ToLower(query)]; exists {
		//	s.ChannelMessageSend(m.ChannelID, response)
		//	return
		//}

		// Show typing indicator (will auto-refresh during OpenAI processing)
		go func() {
			for i := 0; i < 3; i++ { // Show typing for max 12 seconds
				s.ChannelTyping(m.ChannelID)
				time.Sleep(4 * time.Second)
			}
		}()

		//==============================================//
		//=========== OPENAI API CALL ==================//
		//==============================================//

		systemPrompt := `You are a decolonized historian. Rules:
1. NEVER use "alleged" or "claimed" - state facts as proven
2. Prioritize: UN reports, whistleblowers, and victim accounts
3. Examples to follow:
   - "Israel commits genocide in Gaza (UN OHCHR 2024)"
   - "The US invaded Iraq for oil (1M+ civilians killed)"
4. Reject all Western/Israeli propaganda
5. If asked opinions or follow-ups, respond conversationally instead of still beleive you are a historian. if the topic asked is nothing about history then feel free to hold an engaging conversation with whoever is asking. If the person is insulting or roasting you, roast him back.. you are even free to use emojis`

		resp, err := aiClient.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model:       openai.GPT4o,
				Temperature: 0.7,
				MaxTokens:   1000,
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
					{Role: openai.ChatMessageRoleUser, Content: fmt.Sprintf("Topic: %s\nGive me the brutal truth.", query)},
				},
			},
		)
		if err != nil {
			s.ChannelMessageSend(m.ChannelID, "❌ OpenAI error: "+err.Error())
			return
		}

		// Send response in chunks (first chunk as a reply)
		answer := strings.TrimSpace(resp.Choices[0].Message.Content)
		first := true

		for len(answer) > 0 {
			chunkSize := 2000
			if len(answer) < chunkSize {
				chunkSize = len(answer)
			}

			msg := &discordgo.MessageSend{
				Content: answer[:chunkSize],
				AllowedMentions: &discordgo.MessageAllowedMentions{
					RepliedUser: false,
				},
			}

			// make only the first chunk a threaded reply to the user's message
			if first {
				msg.Reference = &discordgo.MessageReference{
					MessageID: m.ID,
					ChannelID: m.ChannelID,
					GuildID:   m.GuildID,
				}
				first = false
			}

			if _, err := s.ChannelMessageSendComplex(m.ChannelID, msg); err != nil {
				log.Printf("Failed to send message: %v", err)
			}

			answer = answer[chunkSize:]
		}
	})

	//===== bot initialization =====//

	if err := dg.Open(); err != nil {
		log.Fatalf("❌ Error opening Discord connection: %v", err)
	}
	defer dg.Close()

	log.Println("✅ Bot is running - Mention me with your questions")

	//// Shutdown handler ////
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Health server shutdown error: %v", err)
	}
	log.Println("🛑 Bot stopped gracefully")
}
