package staff

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"

	"github.com/chazari-x/hmtpk_parser/v2/storage"
)

// Источник полных ФИО — обязательный раздел «Педагогический состав».
// В таблице расписания преподаватели указаны сокращённо («Говорова И.А.»),
// а сайт ХМТПК у автора отдавал полные ФИО, и фронт сокращает их сам (utils.FormatName).
// Поэтому разворачиваем сокращения до полных ФИО — так же, как выглядит оригинал.
const (
	employeesURL = "https://omsk-osma.ru/sveden/employees"
	cacheKey     = "omgmu:staff:names"
	cacheTTL     = 24 * time.Hour
)

var (
	// ФИО: три слова с заглавных, фамилия может быть двойной через дефис.
	nameRe = regexp.MustCompile(`[А-ЯЁ][а-яё]+(?:-[А-ЯЁ][а-яё]+)?\s+[А-ЯЁ][а-яё]+\s+[А-ЯЁ][а-яё]+`)
	client = &http.Client{Timeout: 30 * time.Second}
)

// Controller — справочник полных ФИО преподавателей.
type Controller struct {
	r   *storage.Redis
	log *logrus.Logger
}

func NewController(client *redis.Client, logger *logrus.Logger) *Controller {
	return &Controller{r: &storage.Redis{Redis: client}, log: logger}
}

// Map возвращает карту: нормализованное сокращение -> полное ФИО.
// При ошибке отдаёт пустую карту — тогда имена остаются сокращёнными (плавная деградация).
func (c *Controller) Map(ctx context.Context) map[string]string {
	var m map[string]string
	if ok, _ := c.r.Get(ctx, cacheKey, &m); ok && len(m) > 0 {
		return m
	}

	m, err := c.fetch(ctx)
	if err != nil {
		c.log.Warnf("staff: не удалось получить педсостав: %v", err)
		return map[string]string{}
	}
	_ = c.r.Set(ctx, cacheKey, m, cacheTTL)
	return m
}

func (c *Controller) fetch(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, employeesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; omgmu-schedule/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Сначала собираем все варианты на каждое сокращение, чтобы отсеять неоднозначные.
	tmp := map[string]map[string]bool{}
	for _, raw := range nameRe.FindAllString(string(body), -1) {
		full := strings.Join(strings.Fields(raw), " ")
		if !isFIO(full) {
			continue
		}
		k := Norm(Abbrev(full))
		if tmp[k] == nil {
			tmp[k] = map[string]bool{}
		}
		tmp[k][full] = true
	}

	out := make(map[string]string, len(tmp))
	for k, variants := range tmp {
		if len(variants) != 1 {
			continue // тёзки-однофамильцы: развернуть однозначно нельзя — оставим сокращение
		}
		for full := range variants {
			out[k] = full
		}
	}
	return out, nil
}

// Full разворачивает «Говорова И.А.» -> «Говорова Ирина Анатольевна».
// Если ФИО неизвестно — возвращает исходное сокращение.
func Full(m map[string]string, short string) string {
	if short == "" || len(m) == 0 {
		return short
	}
	if full, ok := m[Norm(short)]; ok {
		return full
	}
	return short
}

// Abbrev: «Говорова Ирина Анатольевна» -> «Говорова И.А.»
func Abbrev(full string) string {
	f := strings.Fields(full)
	if len(f) < 3 {
		return full
	}
	i, o := []rune(f[1]), []rune(f[2])
	return f[0] + " " + string(i[0]) + "." + string(o[0]) + "."
}

// Norm приводит имя к виду, устойчивому к пробелам, регистру и ё/е.
func Norm(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "ё", "е")
	s = strings.ReplaceAll(s, "\u00a0", "")
	return strings.ReplaceAll(s, " ", "")
}

// isFIO отсеивает мусор: отчество почти всегда оканчивается на «ич» или «на».
func isFIO(s string) bool {
	f := strings.Fields(s)
	if len(f) != 3 {
		return false
	}
	return strings.HasSuffix(f[2], "ич") || strings.HasSuffix(f[2], "на")
}
