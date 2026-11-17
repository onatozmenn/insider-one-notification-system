package notification

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTemplate(t *testing.T) {
	valid := TemplateInput{
		Name:    "welcome",
		Channel: ChannelSMS,
		Content: "Hello {{name}}",
	}
	got, err := ValidateTemplate(valid)
	require.NoError(t, err)
	assert.Equal(t, "welcome", got.Name)

	email := TemplateInput{
		Name:    "weekly",
		Channel: ChannelEmail,
		Subject: "Weekly {{name}}",
		Content: "Hello {{name}}",
	}
	_, err = ValidateTemplate(email)
	require.NoError(t, err)

	push := TemplateInput{
		Name:    "push-welcome",
		Channel: ChannelPush,
		Subject: "Welcome",
		Content: "Hello {{name}}",
	}
	_, err = ValidateTemplate(push)
	require.NoError(t, err)
}

func TestValidateTemplateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input TemplateInput
		field string
	}{
		{
			name:  "missing name",
			input: TemplateInput{Channel: ChannelSMS, Content: "hello"},
			field: "name",
		},
		{
			name:  "invalid channel",
			input: TemplateInput{Name: "x", Channel: Channel("fax"), Content: "hello"},
			field: "channel",
		},
		{
			name:  "email without subject",
			input: TemplateInput{Name: "x", Channel: ChannelEmail, Content: "hello"},
			field: "subject",
		},
		{
			name:  "sms with subject",
			input: TemplateInput{Name: "x", Channel: ChannelSMS, Subject: "Hi", Content: "hello"},
			field: "subject",
		},
		{
			name:  "sms content too long",
			input: TemplateInput{Name: "x", Channel: ChannelSMS, Content: strings.Repeat("a", maxSMSContent+1)},
			field: "content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateTemplate(tt.input)
			require.Error(t, err)

			var validationErr *ValidationError
			require.True(t, errors.As(err, &validationErr))
			assert.Contains(t, fieldNames(validationErr), tt.field)
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	subject := "Hello {{name}}"
	template := Template{
		Channel: ChannelEmail,
		Subject: &subject,
		Content: "Hi {{ name }}, your code is {{code}}.",
	}

	rendered, err := RenderTemplate(template, map[string]string{
		"name": "Onat",
		"code": "ABC",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello Onat", rendered.Subject)
	assert.Equal(t, "Hi Onat, your code is ABC.", rendered.Content)
}

func TestRenderTemplateRepeatsPlaceholder(t *testing.T) {
	template := Template{
		Channel: ChannelSMS,
		Content: "{{name}}, hello {{name}}",
	}

	rendered, err := RenderTemplate(template, map[string]string{"name": "Onat"})
	require.NoError(t, err)
	assert.Equal(t, "Onat, hello Onat", rendered.Content)
}

func TestRenderTemplateRejectsTooLongOutput(t *testing.T) {
	template := Template{
		Channel: ChannelSMS,
		Content: "{{body}}",
	}

	_, err := RenderTemplate(template, map[string]string{"body": strings.Repeat("a", maxSMSContent+1)})
	require.Error(t, err)

	var validationErr *ValidationError
	require.True(t, errors.As(err, &validationErr))
	assert.Contains(t, fieldNames(validationErr), "content")
}

func TestRenderTemplateMissingVariable(t *testing.T) {
	template := Template{
		Channel: ChannelSMS,
		Content: "Hello {{name}}",
	}

	_, err := RenderTemplate(template, nil)
	require.Error(t, err)

	var validationErr *ValidationError
	require.True(t, errors.As(err, &validationErr))
	assert.Contains(t, fieldNames(validationErr), "variables.name")
}
