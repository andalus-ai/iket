package compiler

import (
	"fmt"
	"strings"
)

// ErrorType categorizes compiler errors
type ErrorType string

const (
	ErrorTypeValidation  ErrorType = "validation"
	ErrorTypeResolution  ErrorType = "resolution"
	ErrorTypeCompilation ErrorType = "compilation"
	ErrorTypeInternal    ErrorType = "internal"
)

// CompilerError represents a structured compiler error
type CompilerError struct {
	Type     ErrorType              `json:"type"`
	Code     string                 `json:"code"`
	Message  string                 `json:"message"`
	Field    string                 `json:"field,omitempty"`
	Value    interface{}            `json:"value,omitempty"`
	Location *ErrorLocation         `json:"location,omitempty"`
	Details  map[string]interface{} `json:"details,omitempty"`
	Cause    error                  `json:"-"`
}

// ErrorLocation specifies where the error occurred
type ErrorLocation struct {
	Service string `json:"service,omitempty"`
	Route   string `json:"route,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// Error implements the error interface
func (e *CompilerError) Error() string {
	var parts []string

	parts = append(parts, fmt.Sprintf("[%s] %s", e.Type, e.Code))

	if e.Field != "" {
		parts = append(parts, fmt.Sprintf("field=%s", e.Field))
	}

	if e.Location != nil {
		locParts := []string{}
		if e.Location.Service != "" {
			locParts = append(locParts, fmt.Sprintf("service=%s", e.Location.Service))
		}
		if e.Location.Route != "" {
			locParts = append(locParts, fmt.Sprintf("route=%s", e.Location.Route))
		}
		if e.Location.Plugin != "" {
			locParts = append(locParts, fmt.Sprintf("plugin=%s", e.Location.Plugin))
		}
		if len(locParts) > 0 {
			parts = append(parts, strings.Join(locParts, ", "))
		}
	}

	parts = append(parts, e.Message)

	if e.Cause != nil {
		parts = append(parts, fmt.Sprintf("caused by: %v", e.Cause))
	}

	return strings.Join(parts, ": ")
}

// Unwrap returns the underlying error
func (e *CompilerError) Unwrap() error {
	return e.Cause
}

// ErrorList holds multiple compiler errors
type ErrorList struct {
	Errors []*CompilerError `json:"errors"`
}

// Error implements the error interface
func (l *ErrorList) Error() string {
	if len(l.Errors) == 0 {
		return "no errors"
	}

	if len(l.Errors) == 1 {
		return l.Errors[0].Error()
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%d errors:\n", len(l.Errors)))

	for i, err := range l.Errors {
		builder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Error()))
	}

	return builder.String()
}

// Add adds an error to the list
func (l *ErrorList) Add(err *CompilerError) {
	l.Errors = append(l.Errors, err)
}

// HasErrors returns true if the list contains errors
func (l *ErrorList) HasErrors() bool {
	return len(l.Errors) > 0
}

// Filter returns errors matching a predicate
func (l *ErrorList) Filter(predicate func(*CompilerError) bool) *ErrorList {
	filtered := &ErrorList{
		Errors: make([]*CompilerError, 0),
	}

	for _, err := range l.Errors {
		if predicate(err) {
			filtered.Errors = append(filtered.Errors, err)
		}
	}

	return filtered
}

// ByType returns errors of a specific type
func (l *ErrorList) ByType(errorType ErrorType) *ErrorList {
	return l.Filter(func(e *CompilerError) bool {
		return e.Type == errorType
	})
}

// Error constructors

// NewValidationError creates a validation error
func NewValidationError(field, message string) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeValidation,
		Code:    "VALIDATION_FAILED",
		Field:   field,
		Message: message,
		Details: make(map[string]interface{}),
	}
}

// NewResolutionError creates a resolution error
func NewResolutionError(plugin, message string) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeResolution,
		Code:    "PLUGIN_NOT_FOUND",
		Message: message,
		Location: &ErrorLocation{
			Plugin: plugin,
		},
		Details: make(map[string]interface{}),
	}
}

// NewCompilationError creates a compilation error
func NewCompilationError(message string, cause error) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeCompilation,
		Code:    "COMPILATION_FAILED",
		Message: message,
		Cause:   cause,
		Details: make(map[string]interface{}),
	}
}

// WithField adds field information to an error
func (e *CompilerError) WithField(field string) *CompilerError {
	e.Field = field
	return e
}

// WithValue adds value information to an error
func (e *CompilerError) WithValue(value interface{}) *CompilerError {
	e.Value = value
	return e
}

// WithLocation adds location information to an error
func (e *CompilerError) WithLocation(loc *ErrorLocation) *CompilerError {
	e.Location = loc
	return e
}

// WithDetail adds a detail field to an error
func (e *CompilerError) WithDetail(key string, value interface{}) *CompilerError {
	if e.Details == nil {
		e.Details = make(map[string]interface{})
	}
	e.Details[key] = value
	return e
}

// WithCause adds a cause to an error
func (e *CompilerError) WithCause(cause error) *CompilerError {
	e.Cause = cause
	return e
}

// Predefined error codes
const (
	ErrCodeMissingField       = "MISSING_FIELD"
	ErrCodeInvalidValue       = "INVALID_VALUE"
	ErrCodeDuplicateEntry     = "DUPLICATE_ENTRY"
	ErrCodePluginNotFound     = "PLUGIN_NOT_FOUND"
	ErrCodePluginInitFailed   = "PLUGIN_INIT_FAILED"
	ErrCodeInvalidPhase       = "INVALID_PHASE"
	ErrCodeInvalidPriority    = "INVALID_PRIORITY"
	ErrCodeCircularDependency = "CIRCULAR_DEPENDENCY"
	ErrCodeIncompatible       = "INCOMPATIBLE"
)

// Predefined error creators

// ErrMissingField creates a missing field error
func ErrMissingField(field string) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeValidation,
		Code:    ErrCodeMissingField,
		Field:   field,
		Message: fmt.Sprintf("required field is missing: %s", field),
	}
}

// ErrInvalidValue creates an invalid value error
func ErrInvalidValue(field string, value interface{}, reason string) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeValidation,
		Code:    ErrCodeInvalidValue,
		Field:   field,
		Value:   value,
		Message: fmt.Sprintf("invalid value for %s: %s", field, reason),
	}
}

// ErrDuplicateEntry creates a duplicate entry error
func ErrDuplicateEntry(entryType, name string) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeValidation,
		Code:    ErrCodeDuplicateEntry,
		Message: fmt.Sprintf("duplicate %s: %s", entryType, name),
	}
}

// ErrPluginNotFound creates a plugin not found error
func ErrPluginNotFound(plugin string) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeResolution,
		Code:    ErrCodePluginNotFound,
		Message: fmt.Sprintf("plugin not found: %s", plugin),
		Location: &ErrorLocation{
			Plugin: plugin,
		},
	}
}

// ErrPluginInitFailed creates a plugin initialization error
func ErrPluginInitFailed(plugin string, cause error) *CompilerError {
	return &CompilerError{
		Type:    ErrorTypeResolution,
		Code:    ErrCodePluginInitFailed,
		Message: fmt.Sprintf("failed to initialize plugin: %s", plugin),
		Cause:   cause,
		Location: &ErrorLocation{
			Plugin: plugin,
		},
	}
}

// FormatErrorList formats an error list for display
func FormatErrorList(errors *ErrorList) string {
	if !errors.HasErrors() {
		return "No errors"
	}

	var builder strings.Builder

	// Group by type
	byType := make(map[ErrorType][]*CompilerError)
	for _, err := range errors.Errors {
		byType[err.Type] = append(byType[err.Type], err)
	}

	// Format by type
	typeOrder := []ErrorType{
		ErrorTypeValidation,
		ErrorTypeResolution,
		ErrorTypeCompilation,
		ErrorTypeInternal,
	}

	for _, errType := range typeOrder {
		errs, exists := byType[errType]
		if !exists || len(errs) == 0 {
			continue
		}

		builder.WriteString(fmt.Sprintf("\n%s Errors (%d):\n", errType, len(errs)))
		for i, err := range errs {
			builder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Message))
			if err.Field != "" {
				builder.WriteString(fmt.Sprintf("     Field: %s\n", err.Field))
			}
			if err.Location != nil {
				if err.Location.Service != "" {
					builder.WriteString(fmt.Sprintf("     Service: %s\n", err.Location.Service))
				}
				if err.Location.Route != "" {
					builder.WriteString(fmt.Sprintf("     Route: %s\n", err.Location.Route))
				}
			}
		}
	}

	return builder.String()
}
