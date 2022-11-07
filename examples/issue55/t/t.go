package t

import (
	"fmt"
	"github.com/pkujhd/goloader/examples/issue55/p"
)

func Test(param p.Intf) p.Intf {
	param.Print("Intf")
	fmt.Println("done!")
	return param
}
