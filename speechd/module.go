/*
 * Copyright (c)  2024  All Rights Reserved
 * 项目名称:edgetts-speechd
 * 文件名称:module.go
 * 修改日期:2024/05/24 14:07:09
 * 作者:kerojiang
 */

package speechd

import (
	"fmt"

	"github.com/ilyapashuk/go-speechd"
)

func Speak() {
	con, err := speechd.Open()
	if err != nil {
		fmt.Println(err)
	}
	con.SetOutputModule()
}
