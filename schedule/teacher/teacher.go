package teacher

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"

	"github.com/vanyayudin26/omgmu_parser/model"
	"github.com/vanyayudin26/omgmu_parser/schedule"
	"github.com/vanyayudin26/omgmu_parser/schedule/group"
	"github.com/vanyayudin26/omgmu_parser/schedule/staff"
	"github.com/vanyayudin26/omgmu_parser/storage"
)

const optsTTL = 6 * time.Hour

// Controller — источник расписания по преподавателям.
// Расписания преподавателя на сайте нет: оно собирается из расписаний всех групп.
type Controller struct {
	r      *storage.Redis
	log    *logrus.Logger
	groups *group.Controller
	staff  *staff.Controller
}

// NewController принимает готовый group.Controller, чтобы переиспользовать его кэш групп.
func NewController(groups *group.Controller, staffCtrl *staff.Controller, client *redis.Client, logger *logrus.Logger) *Controller {
	return &Controller{r: &storage.Redis{Redis: client}, log: logger, groups: groups, staff: staffCtrl}
}

// GetOptions собирает список преподавателей из всех групп.
// Value — сокращение из таблицы расписания (стабильный ключ, по нему идёт отбор пар),
// Label — полное ФИО из педсостава (фронт сам сократит его для чипа) — как в оригинале.
func (c *Controller) GetOptions(ctx context.Context) ([]model.Option, error) {
	const key = "omgmu:options:teachers"
	var opts []model.Option
	if ok, _ := c.r.Get(ctx, key, &opts); ok && len(opts) > 0 {
		return opts, nil
	}

	groups, err := c.groups.GetOptions(ctx)
	if err != nil {
		return nil, err
	}

	set := map[string]bool{}
	for _, g := range groups {
		days, err := c.groups.Days(ctx, g.Value)
		if err != nil {
			c.log.Warnf("teacher options: группа %s: %v", g.Value, err)
			continue
		}
		for _, d := range days {
			for _, cell := range d.Cells {
				for _, t := range cell.Teachers {
					if t = strings.TrimSpace(t); t != "" {
						set[t] = true
					}
				}
			}
		}
	}

	names := c.staff.Map(ctx)
	opts = make([]model.Option, 0, len(set))
	for t := range set {
		opts = append(opts, model.Option{Label: staff.Full(names, t), Value: t})
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].Label < opts[j].Label })
	_ = c.r.Set(ctx, key, opts, optsTTL)
	return opts, nil
}

// GetSchedule собирает расписание преподавателя на неделю, содержащую date (ровно 7 дней Пн..Вс),
// проходя по всем группам и отбирая пары, где встречается этот преподаватель.
// Lesson.Teacher намеренно не заполняется — в оригинале в расписании преподавателя
// карточка показывает только группу (имя и так в чипе).
func (c *Controller) GetSchedule(ctx context.Context, teacher, date string) ([]model.Schedule, error) {
	groups, err := c.groups.GetOptions(ctx)
	if err != nil {
		return nil, err
	}

	// Сырые пары преподавателя по дате "dd/mm" (без Num — нумеруем ниже по времени).
	raw := make(map[string][]model.Lesson)
	for _, g := range groups {
		days, err := c.groups.Days(ctx, g.Value)
		if err != nil {
			c.log.Warnf("teacher schedule: группа %s: %v", g.Value, err)
			continue
		}
		for _, d := range days {
			for _, cell := range d.Cells {
				for k, t := range cell.Teachers {
					if t != teacher {
						continue
					}
					sub := ""
					if len(cell.Teachers) > 1 {
						sub = strconv.Itoa(k + 1)
					}
					raw[d.Date] = append(raw[d.Date], model.Lesson{
						Time: cell.Time, Name: cell.Subject,
						Room: schedule.At(cell.Rooms, k), Group: g.Value,
						Subgroup: sub,
					})
				}
			}
		}
	}

	// Нумеруем пары внутри дня по времени (строки "HH:MM..." сравнимы лексикографически).
	byDay := make(map[string][]model.Lesson, len(raw))
	for date, lessons := range raw {
		sort.SliceStable(lessons, func(i, j int) bool { return lessons[i].Time < lessons[j].Time })
		num, prev := 0, ""
		for i := range lessons {
			if lessons[i].Time != prev {
				num++
				prev = lessons[i].Time
			}
			lessons[i].Num = strconv.Itoa(num)
		}
		byDay[date] = lessons
	}

	// У преподавателя своей страницы на сайте нет — кнопку «на сайте» ведём на индекс расписания.
	return schedule.Assemble(date, byDay, schedule.IndexURL()), nil
}
