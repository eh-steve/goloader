package test_issue71

type Farmer struct {
	Name *string
}

func (f *Farmer) Say(word string) {
	f.Name = &word
}
func (f *Farmer) Get() string {
	return *f.Name
}
func New() interface{} {
	return &Farmer{}
}
