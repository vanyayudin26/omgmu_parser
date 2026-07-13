// Модуль-форк парсера расписания под колледж ОмГМУ.
// Путь модуля намеренно совпадает с оригиналом (github.com/chazari-x/hmtpk_parser/v2),
// чтобы hmtpk_schedule_api подключил его через директиву replace без правок .go-файлов:
//
//	// в go.mod сервера:
//	replace github.com/chazari-x/hmtpk_parser/v2 => ../omgmu_parser
//
// При публикации под своим именем — сменить module и импорты (sed) и убрать replace.
module github.com/chazari-x/hmtpk_parser/v2

go 1.21

require (
	github.com/PuerkitoBio/goquery v1.9.2
	github.com/go-redis/redis/v8 v8.11.5
	github.com/sirupsen/logrus v1.9.3
)

require (
	github.com/andybalholm/cascadia v1.3.2 // indirect
	github.com/cespare/xxhash/v2 v2.1.2 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	golang.org/x/net v0.24.0 // indirect
	golang.org/x/sys v0.19.0 // indirect
)
