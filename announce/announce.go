package announce

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/chazari-x/hmtpk_parser/v2/model"
)

// Announce — блок объявлений.
// Источник новостей для колледжа ОмГМУ пока не определён, поэтому это заглушка,
// сохраняющая контракт оригинала. Позже сюда добавится парсинг новостей.
type Announce struct {
	log *logrus.Logger
}

func NewAnnounce(logger *logrus.Logger) *Announce {
	return &Announce{log: logger}
}

// GetAnnounces возвращает пустой список объявлений (last_page = 1).
func (a *Announce) GetAnnounces(_ context.Context, _ int) (model.Announces, error) {
	return model.Announces{Announces: []model.Announce{}, LastPage: 1}, nil
}
