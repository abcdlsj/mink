package llm

import openrouter "github.com/revrost/go-openrouter"

func streamOptions() *openrouter.StreamOptions {
	return &openrouter.StreamOptions{IncludeUsage: true}
}
