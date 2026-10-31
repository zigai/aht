package harness

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

var (
	hookPayloadValidatorOnce sync.Once
	hookPayloadValidate      *validator.Validate
)

type HookPayloadValidator func(json.RawMessage) bool

func PayloadValidator[T any]() HookPayloadValidator {
	return func(rawPayload json.RawMessage) bool {
		var payload T
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			return false
		}

		return hookPayloadValidator().Struct(payload) == nil
	}
}

func PayloadObject(rawPayload json.RawMessage) (map[string]any, bool) {
	var payload map[string]any
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return nil, false
	}

	if payload == nil {
		return nil, false
	}

	return payload, true
}

func hookPayloadValidator() *validator.Validate {
	hookPayloadValidatorOnce.Do(func() {
		hookPayloadValidate = validator.New(validator.WithRequiredStructEnabled())
		if err := hookPayloadValidate.RegisterValidation("notblank", validateNotBlank); err != nil {
			panic(err)
		}
	})

	return hookPayloadValidate
}

func validateNotBlank(field validator.FieldLevel) bool {
	value, ok := validationString(field.Field())
	return ok && value != ""
}

func validationString(value reflect.Value) (string, bool) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", false
		}

		value = value.Elem()
	}

	if value.Kind() != reflect.String {
		return "", false
	}

	return strings.TrimSpace(value.String()), true
}
