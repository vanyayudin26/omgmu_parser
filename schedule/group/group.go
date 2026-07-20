package group

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"

	"github.com/vanyayudin26/omgmu_parser/errors"
	"github.com/vanyayudin26/omgmu_parser/model"
	"github.com/vanyayudin26/omgmu_parser/schedule"
	"github.com/vanyayudin26/omgmu_parser/schedule/staff"
	"github.com/vanyayudin26/omgmu_parser/storage"
)

// TODO: переделать кэш на метку версии расписания.
// На индексной странице есть строка «Расписание сформировано: 15.06.2026 13:35» —
// она меняется при каждом переформировании. План: кэшировать метку коротко (~5 мин),
// а страницы групп класть под ключ «omgmu:days:<метка>:<группа>» с длинным TTL.
// Тогда изменения подхватываются в течение 5 минут, а неизменное расписание
// не перезапрашивается вовсе (сейчас все 137 групп перечитываются каждые 30 минут впустую).
const (
	daysTTL = 30 * time.Minute
	optsTTL = 6 * time.Hour
)

// Controller — источник расписания по группам.
type Controller struct {
	r     *storage.Redis
	log   *logrus.Logger
	staff *staff.Controller
}

func NewController(staffCtrl *staff.Controller, client *redis.Client, logger *logrus.Logger) *Controller {
	return &Controller{r: &storage.Redis{Redis: client}, log: logger, staff: staffCtrl}
}

// GetOptions возвращает список групп с индексной страницы.
func (c *Controller) GetOptions(ctx context.Context) ([]model.Option, error) {
	const key = "omgmu:options:groups"
	var opts []model.Option
	if ok, _ := c.r.Get(ctx, key, &opts); ok && len(opts) > 0 {
		return opts, nil
	}

	doc, err := schedule.Fetch(ctx, schedule.IndexURL())
	if err != nil {
		return nil, err
	}
	opts = schedule.ParseOptions(doc)
	if len(opts) == 0 {
		return nil, errors.ErrorBadResponse
	}
	_ = c.r.Set(ctx, key, opts, optsTTL)
	return opts, nil
}

// Days возвращает все распарсенные дни группы (все недели). Кэшируется.
// Используется и адаптером группы, и адаптером преподавателя.
func (c *Controller) Days(ctx context.Context, name string) ([]schedule.Day, error) {
	key := "omgmu:days:group:" + name
	var days []schedule.Day
	if ok, _ := c.r.Get(ctx, key, &days); ok {
		return days, nil
	}

	doc, err := schedule.Fetch(ctx, schedule.GroupURL(name))
	if err != nil {
		return nil, err
	}
	days = schedule.ParseDays(doc)
	_ = c.r.Set(ctx, key, days, daysTTL)
	return days, nil
}

// GetSchedule возвращает расписание группы на неделю, содержащую date (ровно 7 дней Пн..Вс).
// Преподаватели разворачиваются в полные ФИО — как в оригинале (фронт сокращает их сам).
func (c *Controller) GetSchedule(ctx context.Context, name, date string) ([]model.Schedule, error) {
	days, err := c.Days(ctx, name)
	if err != nil {
		return nil, err
	}
	names := c.staff.Map(ctx)

	byDay := make(map[string][]model.Lesson)
	for _, d := range days {
		num := 0
		for _, cell := range d.Cells {
			num++
			// Group намеренно пустой: в расписании группы код группы не дублируется —
			// он и так выбран в чипе (в оригинале колонки «Группа» в таблице группы нет).
			lessons := schedule.ExpandCell(cell, num, "")
			for i := range lessons {
				lessons[i].Teacher = staff.Full(names, lessons[i].Teacher)
			}
			byDay[d.Date] = append(byDay[d.Date], lessons...)
		}
	}
	return schedule.Assemble(date, byDay, schedule.GroupURL(name)), nil
}
