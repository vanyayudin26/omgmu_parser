package staff

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"

	"github.com/vanyayudin26/omgmu_parser/storage"
)

// Источники полных ФИО.
//
// В таблице расписания преподаватели указаны сокращённо («Клещ Г.Р.»), а сайт ХМТПК
// у автора отдавал полные ФИО (фронт сокращает их сам — utils.FormatName).
// Поэтому разворачиваем сокращения до полных ФИО — чтобы выглядело как в оригинале.
//
// Список зашит намеренно: суффиксы страниц сотрудников на сайте не унифицированы
// (/sotrudniki, /sotrudniki-cmk, /sotrudniki-cmk-<название>, /sotrudniki-otdeleniya),
// поэтому вывести их шаблоном нельзя — адреса собраны по ссылкам с сайта и проверены.
// Если страница переедет, её ФИО просто останутся сокращёнными (плавная деградация).
var sources = []string{
	// Педсостав вуза и колледжа — основная масса (~750 ФИО).
	"https://omsk-osma.ru/sveden/employees",

	// Отделения и ЦМК колледжа — те, кого нет в реестре педсостава.
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-farmaciya/cmk-himicheskie-discipliny/sotrudniki-cmk-himicheskie-discipliny",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-farmaciya/cmk-inostrannye-yazyki/sotrudniki-cmk-inostrannye-yazyki",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-farmaciya/sotrudniki-otdeleniya",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-laboratornaya-diagnostika/cmk-laboratornye-metody-issledovaniya/sotrudniki-cmk",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-laboratornaya-diagnostika/sotrudniki-otdeleniya",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-lechebnoe-delo/cmk-fundamental-nye-discipliny/sotrudniki",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-lechebnoe-delo/cmk-hirurgicheskie-discipliny/sotrudniki-cmk-hirurgicheskie-discipliny",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-lechebnoe-delo/cmk-medicinskaya-optika/sotrudniki",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-lechebnoe-delo/cmk-obschie-gumanitarnye-i-social-no-ekonomicheskie-discipliny/sotrudniki-cmk",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-lechebnoe-delo/cmk-terapevticheskie-discipliny/sotrudniki-cmk",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-lechebnoe-delo/sotrudniki-otdeleniya",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-sestrinskoe-delo/cmk-fizicheskaya-kul-tura/sotrudniki",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-sestrinskoe-delo/cmk-osnovy-sestrinskogo-dela/sotrudniki",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-sestrinskoe-delo/cmk-sestrinskoe-delo-v-klinicheskih-disciplinah/sotrudniki-cmk",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/otdelenie-sestrinskoe-delo/sotrudniki-otdeleniya",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/special-nost-medicinskaya-optika/sotrudniki",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdeleniya/special-nost-mediko-profilakticheskoe-delo/sotrudniki",

	// Отделы колледжа — сотрудники (психологи, методисты), которые тоже ведут занятия.
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdely-kolledzha/otdel-po-vospitatel-noy-rabote",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdely-kolledzha/uchebno-metodicheskiy-otdel",
	"https://omsk-osma.ru/obrazovanie/kolledzh/otdely-kolledzha/uchebno-proizvodstvennyy-otdel",
}

const (
	cacheKey = "omgmu:staff:names"
	cacheTTL = 24 * time.Hour
	workers  = 3
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
// Кэшируется на сутки. При ошибках отдаёт то, что удалось собрать
// (в пределе — пустую карту, тогда имена остаются сокращёнными).
func (c *Controller) Map(ctx context.Context) map[string]string {
	var m map[string]string
	if ok, _ := c.r.Get(ctx, cacheKey, &m); ok && len(m) > 0 {
		return m
	}

	m = c.collect(ctx)
	if len(m) > 0 {
		_ = c.r.Set(ctx, cacheKey, m, cacheTTL)
	}
	return m
}

func (c *Controller) collect(ctx context.Context) map[string]string {
	var (
		mu  sync.Mutex
		all = map[string]bool{}
		wg  sync.WaitGroup
	)
	sem := make(chan struct{}, workers)

	for _, url := range sources {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			names, err := c.fetch(ctx, url)
			if err != nil {
				c.log.Warnf("staff: %s: %v", url, err)
				return
			}
			mu.Lock()
			for _, n := range names {
				all[n] = true
			}
			mu.Unlock()
		}(url)
	}
	wg.Wait()

	// Сначала собираем все варианты на каждое сокращение, чтобы отсеять неоднозначные.
	tmp := map[string]map[string]bool{}
	for full := range all {
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
	return out
}

func (c *Controller) fetch(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; omgmu-schedule/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil // страница переехала или пропала — просто пропускаем
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, raw := range nameRe.FindAllString(string(body), -1) {
		full := strings.Join(strings.Fields(raw), " ")
		if isFIO(full) {
			out = append(out, full)
		}
	}
	return out, nil
}

// Full разворачивает «Клещ Г.Р.» -> «Клещ Гузяль Разильевна».
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

// Abbrev: «Клещ Гузяль Разильевна» -> «Клещ Г.Р.»
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
