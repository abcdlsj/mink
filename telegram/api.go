package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// TelegramAPI Telegram API 封装
type TelegramAPI struct {
	token  string
	client *http.Client
}

func (api *TelegramAPI) baseURL() string {
	return fmt.Sprintf("https://api.telegram.org/bot%s", api.token)
}

func (api *TelegramAPI) sendMessage(chatID int64, text string) error {
	u := fmt.Sprintf("%s/sendMessage", api.baseURL())
	
	data := map[string]interface{}{
		"chat_id": chatID,
		"text":    text,
		"parse_mode": "Markdown",
	}
	
	jsonData, _ := json.Marshal(data)
	resp, err := api.client.Post(u, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	return nil
}

func (api *TelegramAPI) getUpdates(offset int) ([]Update, error) {
	u := fmt.Sprintf("%s/getUpdates?offset=%d&limit=100", api.baseURL(), offset)
	
	resp, err := api.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	var result struct {
		OK      bool     `json:"ok"`
		Result  []Update `json:"result"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result.Result, nil
}

func (api *TelegramAPI) answerCallbackQuery(queryID string) error {
	u := fmt.Sprintf("%s/answerCallbackQuery", api.baseURL())
	
	data := map[string]string{
		"callback_query_id": queryID,
	}
	
	jsonData, _ := json.Marshal(data)
	resp, err := api.client.Post(u, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	return nil
}

// SetWebhook 设置 webhook
func (api *TelegramAPI) SetWebhook(webhookURL string) error {
	u := fmt.Sprintf("%s/setWebhook", api.baseURL())
	
	data := url.Values{}
	data.Set("url", webhookURL)
	
	resp, err := api.client.PostForm(u, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	return nil
}

// DeleteWebhook 删除 webhook
func (api *TelegramAPI) DeleteWebhook() error {
	u := fmt.Sprintf("%s/deleteWebhook", api.baseURL())
	
	resp, err := api.client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	return nil
}
