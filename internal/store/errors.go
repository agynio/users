package store

import "fmt"

type NotFoundError struct {
	Resource string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s not found", e.Resource)
}

type AlreadyExistsError struct {
	Resource string
}

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("%s already exists", e.Resource)
}

type ExpiredError struct {
	Resource string
}

func (e *ExpiredError) Error() string {
	return fmt.Sprintf("%s expired", e.Resource)
}

func NotFound(resource string) error {
	return &NotFoundError{Resource: resource}
}

func AlreadyExists(resource string) error {
	return &AlreadyExistsError{Resource: resource}
}

func Expired(resource string) error {
	return &ExpiredError{Resource: resource}
}
