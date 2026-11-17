package notification

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// Template is a reusable message body with placeholders.
type Template struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Channel   Channel   `json:"channel"`
	Subject   *string   `json:"subject"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TemplateInput is the request body for creating a template.
type TemplateInput struct {
	Name    string  `json:"name"`
	Channel Channel `json:"channel"`
	Subject string  `json:"subject,omitempty"`
	Content string  `json:"content"`
}

// RenderedTemplate is the result after variable substitution.
type RenderedTemplate struct {
	Subject string
	Content string
}

// ValidateTemplate normalizes and validates a template.
func ValidateTemplate(input TemplateInput) (TemplateInput, error) {
	var errs []FieldError

	input.Name = strings.TrimSpace(input.Name)
	input.Channel = Channel(strings.ToLower(strings.TrimSpace(string(input.Channel))))
	input.Subject = strings.TrimSpace(input.Subject)

	if input.Name == "" {
		errs = append(errs, FieldError{Field: "name", Reason: "is required"})
	} else if len([]rune(input.Name)) > 100 {
		errs = append(errs, FieldError{Field: "name", Reason: "must be at most 100 characters"})
	}

	if !input.Channel.Valid() {
		errs = append(errs, FieldError{Field: "channel", Reason: "must be one of sms, email, push"})
	}

	if strings.TrimSpace(input.Content) == "" {
		errs = append(errs, FieldError{Field: "content", Reason: "is required"})
	} else {
		limit := maxPushContent
		switch input.Channel {
		case ChannelSMS:
			limit = maxSMSContent
		case ChannelEmail:
			limit = maxEmailContent
		case ChannelPush:
			limit = maxPushContent
		}
		if len([]rune(input.Content)) > limit {
			errs = append(errs, FieldError{Field: "content", Reason: "must fit the channel limit"})
		}
	}

	switch input.Channel {
	case ChannelSMS:
		if input.Subject != "" {
			errs = append(errs, FieldError{Field: "subject", Reason: "is not allowed for sms"})
		}
	case ChannelEmail:
		if input.Subject == "" {
			errs = append(errs, FieldError{Field: "subject", Reason: "is required for email"})
		} else if len([]rune(input.Subject)) > maxEmailSubject {
			errs = append(errs, FieldError{Field: "subject", Reason: "must be at most 200 characters for email"})
		}
	case ChannelPush:
		if len([]rune(input.Subject)) > maxPushSubject {
			errs = append(errs, FieldError{Field: "subject", Reason: "must be at most 100 characters for push"})
		}
	}

	if len(errs) > 0 {
		return TemplateInput{}, &ValidationError{Details: errs}
	}

	return input, nil
}

// RenderTemplate replaces {{variable}} placeholders.
func RenderTemplate(template Template, variables map[string]string) (RenderedTemplate, error) {
	missing := make(map[string]struct{})

	render := func(text string) string {
		return placeholderPattern.ReplaceAllStringFunc(text, func(match string) string {
			parts := placeholderPattern.FindStringSubmatch(match)
			if len(parts) < 2 {
				return match
			}
			name := parts[1]
			value, ok := variables[name]
			if !ok {
				missing[name] = struct{}{}
				return match
			}
			return value
		})
	}

	subject := ""
	if template.Subject != nil {
		subject = render(*template.Subject)
	}
	content := render(template.Content)

	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for name := range missing {
			names = append(names, name)
		}
		sort.Strings(names)

		details := make([]FieldError, 0, len(names))
		for _, name := range names {
			details = append(details, FieldError{
				Field:  "variables." + name,
				Reason: "is required",
			})
		}
		return RenderedTemplate{}, &ValidationError{Details: details}
	}

	return RenderedTemplate{Subject: subject, Content: content}, nil
}
