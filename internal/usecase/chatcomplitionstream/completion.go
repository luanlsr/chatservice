package chatcomplitionstream

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/luanlsr/chatservice/internal/domain/entity"
	"github.com/luanlsr/chatservice/internal/domain/gateway"
	"github.com/sashabaranov/go-openai"
)

type ChatCompletionConfigInputDTO struct {
	Model string
	ModelMaxToken int
	Temperature float32
	TopP float32
	N int
	Stop []string
	MaxTokens int
	PresencePenalty float32
	FrequencyePenalty float32
	InitialSystemMessage string
}

type ChatCompletionInputDTO struct{
	ChatID string
	UserID string
	UserMessage string
	Config ChatCompletionConfigInputDTO
}

type ChatCompletionOutputDTO struct{
	ChatID string
	UserID string
	Content string
}

type ChatCompletionUseCase struct {
	ChatGateway gateway.ChatGateway
	OpenAiClient *openai.Client
	Stream chan ChatCompletionOutputDTO
}

func newChatCompletionUseCase(chatGateway gateway.ChatGateway, openAiClient *openai.Client, stream chan ChatCompletionOutputDTO) (*ChatCompletionUseCase) {
	return &ChatCompletionUseCase{
		ChatGateway: chatGateway,
		OpenAiClient: openAiClient,
		Stream: stream,
	}
}

func (uc *ChatCompletionUseCase) Execute(ctx context.Context, input ChatCompletionInputDTO) (*ChatCompletionOutputDTO, error) {
	chat, err  := uc.ChatGateway.FindChatByID(ctx, input.ChatID)
	if err != nil {
		if err.Error() == "chat not found" {
			// create new chat (entity)
			chat, err = createNewChat(input)
			if err != nil {
				return nil, errors.New("error creating new chat: " + err.Error())
			}
			// save on database***
			err = uc.ChatGateway.CreateChat(ctx, chat)
			if err != nil {
				return nil, errors.New("error persisting new chat: " + err.Error())
			}
		} else {
			return nil, errors.New("error fetching existing chat: " + err.Error())
		}
	}
	userMessage, err := entity.NewMessage("user", input.UserMessage, chat.Config.Model)
	if err != nil {
		return nil, errors.New("error creating user message: " + err.Error())
	}
	err = chat.AddMessage(userMessage)
	if err != nil {
		return nil, errors.New("error adding new message: " + err.Error())
	}

	messages := []openai.ChatCompletionMessage{}
	for _, msg := range chat.Messages {
		messages = append(messages, openai.ChatCompletionMessage{
			Role: msg.Role,
			Content: msg.Content,
		})
	}

	resp, err := uc.OpenAiClient.CreateChatCompletionStream(
		ctx,
		openai.ChatCompletionRequest{
			Model: chat.Config.Model.Name,
			Messages: messages,
			MaxTokens: chat.Config.MaxTokens,
			Temperature: chat.Config.Temperature,
			TopP: chat.Config.TopP,
			Stop: chat.Config.Stop,
			PresencePenalty: chat.Config.PresencePenalty,
			FrequencyPenalty: chat.Config.PresencePenalty,
			Stream: false,
		}, 
	)
	if err != nil {
		return nil, errors.New("error creating chat completion: " + err.Error())
	}

	var fullResponse strings.Builder

	for {
		response, err := resp.Recv()
		if errors.Is(err, io.EOF){
			break
		}
		if err != nil {
			return nil, errors.New("error straming response: " + err.Error())
		}
		fullResponse.WriteString((response.Choices[0].Delta.Content))
		r := ChatCompletionOutputDTO{
			ChatID: chat.ID,
			UserID: chat.UserID,
			Content: fullResponse.String(),
		}
		uc.Stream <- r
	}

	assistant, err := entity.NewMessage("assistant", fullResponse.String(), chat.Config.Model)
	if err != nil {
		return nil, errors.New("error creating assistant message: " + err.Error())
	}

	err = chat.AddMessage(assistant)
	if err != nil {
		return nil, errors.New("error adding new mesage: " + err.Error())
	}

	err = uc.ChatGateway.SaveChat(ctx, chat)
	if err != nil {
		return nil, errors.New("error saving chat: " + err.Error())
	}
	return &ChatCompletionOutputDTO{
		ChatID: input.ChatID,
		UserID: chat.UserID,
		Content: fullResponse.String(),
	}, nil
}

func createNewChat(input ChatCompletionInputDTO) (*entity.Chat, error) {
	model := entity.NewModel(input.Config.Model, input.Config.ModelMaxToken)
	chatConfig := &entity.ChatConfig {
		Temperature: input.Config.Temperature,
		TopP: input.Config.TopP,
		N: input.Config.N,
		Stop: input.Config.Stop,
		MaxTokens: input.Config.MaxTokens,
		PresencePenalty: input.Config.PresencePenalty,
		FrequencyePenalty: input.Config.FrequencyePenalty,
		Model: model,
	}
	initialMessage, err := entity.NewMessage("system", input.Config.InitialSystemMessage, model)
	if err != nil {
		return nil, errors.New("error creating initial message: " + err.Error())
	}
	chat, err := entity.NewChat(input.UserID, initialMessage, chatConfig)
	if err != nil {
		return nil, errors.New("error creating new chat: " + err.Error())
	}
	return chat, nil
}