// Живой сквозной тест библиотеки без Redis (кэш отключён — client nil).
//
//	go run ./cmd/dump groups                     # список групп
//	go run ./cmd/dump teachers                    # список преподавателей (обходит все группы!)
//	go run ./cmd/dump group 402Ф                   # расписание группы (текущая неделя)
//	go run ./cmd/dump group 402Ф 20.01.2026        # ... неделя с указанной датой
//	go run ./cmd/dump teacher "Говорова И.А." 20.01.2026
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	hmtpk "github.com/vanyayudin26/omgmu_parser"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: dump <groups|teachers|group|teacher> [name] [date]")
		os.Exit(1)
	}

	log := logrus.New()
	c := hmtpk.NewController(nil, log) // nil redis -> кэш отключён
	ctx := context.Background()

	mode := os.Args[1]
	arg := func(i int) string {
		if len(os.Args) > i {
			return os.Args[i]
		}
		return ""
	}

	var (
		v   any
		err error
	)
	switch mode {
	case "groups":
		v, err = c.GetGroupOptions(ctx)
	case "teachers":
		v, err = c.GetTeacherOptions(ctx)
	case "group":
		v, err = c.GetScheduleByGroup(arg(2), arg(3), ctx)
	case "teacher":
		v, err = c.GetScheduleByTeacher(arg(2), arg(3), ctx)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", mode)
		os.Exit(1)
	}
	if err != nil {
		panic(err)
	}

	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}
