package errors

import (
	errs "errors"
)

var (
	ErrorBadResponse = errs.New("Неверный ответ от https://omsk-osma.ru")
	ErrorBadRequest  = errs.New("Неверный запрос")
)
