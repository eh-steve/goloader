package test_issue96

import "fmt"

type (
	IDo interface {
		Do() error
	}
	Struct1 struct{}
)

func (Struct1) Do() error {
	fmt.Print("DO SOMETHING\n")
	return nil
}

func NewStructX() IDo {
	return Struct1{}
}
