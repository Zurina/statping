package services

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"github.com/hunterlong/statping/types/null"
	"github.com/hunterlong/statping/utils"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const limitedFailures = 25

func (s *Service) Duration() time.Duration {
	return time.Duration(s.Interval) * time.Second
}

// Start will create a channel for the service checking go routine
func (s *Service) Start() {
	if s.IsRunning() {
		return
	}
	s.Running = make(chan bool)
}

// Close will stop the go routine that is checking if service is online or not
func (s *Service) Close() {
	if s.IsRunning() {
		close(s.Running)
	}
}

// IsRunning returns true if the service go routine is running
func (s *Service) IsRunning() bool {
	if s.Running == nil {
		return false
	}
	select {
	case <-s.Running:
		return false
	default:
		return true
	}
}

func (s Service) Hash() string {
	format := fmt.Sprintf("name:%sdomain:%sport:%dtype:%smethod:%s", s.Name, s.Domain, s.Port, s.Type, s.Method)
	h := sha1.New()
	h.Write([]byte(format))
	return hex.EncodeToString(h.Sum(nil))
}

// SelectAllServices returns a slice of *core.Service to be store on []*core.Services
// should only be called once on startup.
func SelectAllServices(start bool) (map[int64]*Service, error) {
	if len(allServices) > 0 {
		return allServices, nil
	}

	for _, s := range all() {

		allServices[s.Id] = s

		if start {
			CheckinProcess(s)
		}

		fails := s.AllFailures().Last(limitedFailures)
		s.Failures = fails

		for _, c := range s.Checkins() {
			s.AllCheckins = append(s.AllCheckins, c)
		}

		// collect initial service stats
		s.UpdateStats()
	}

	return allServices, nil
}

func ValidateService(line string) (*Service, error) {
	p, err := url.Parse(line)
	if err != nil {
		return nil, err
	}
	newService := new(Service)

	domain := p.Host
	newService.Name = niceDomainName(domain, p.Path)
	if p.Port() != "" {
		newService.Port = int(utils.ToInt(p.Port()))
		if p.Scheme != "http" && p.Scheme != "https" {
			domain = strings.ReplaceAll(domain, ":"+p.Port(), "")
		}
	}
	newService.Domain = domain

	switch p.Scheme {
	case "http", "https":
		newService.Type = "http"
		newService.Method = "get"
		if p.Scheme == "https" {
			newService.VerifySSL = null.NewNullBool(true)
		}
	default:
		newService.Type = p.Scheme
	}
	return newService, nil
}

func niceDomainName(domain string, paths string) string {
	domain = strings.ReplaceAll(domain, "www.", "")
	splitPath := strings.Split(paths, "/")
	if len(splitPath) == 1 {
		return domain
	}
	var addedName []string
	for k, p := range splitPath {
		if k > 2 {
			break
		}
		if len(p) > 16 {
			addedName = append(addedName, p+"...")
			break
		} else {
			addedName = append(addedName, p)
		}
	}
	return domain + strings.Join(addedName, "/")
}

func (s *Service) UpdateStats() *Service {
	s.Online24Hours = s.OnlineDaysPercent(1)
	s.Online7Days = s.OnlineDaysPercent(7)
	s.AvgResponse = s.AvgTime()
	s.FailuresLast24Hours = len(s.AllFailures().Since(utils.Now().Add(-time.Hour * 24)))

	if s.LastOffline.IsZero() {
		lastFail := s.LastFailure()
		if lastFail != nil {
			s.LastOffline = s.LastFailure().CreatedAt
		}
	}

	s.Stats = &Stats{
		Failures: s.AllFailures().Count(),
		Hits:     s.AllHits().Count(),
		FirstHit: s.FirstHit().CreatedAt,
	}
	return s
}

// AvgTime will return the average amount of time for a service to response back successfully
func (s *Service) AvgTime() int64 {
	return s.AllHits().Avg()
}

// OnlineDaysPercent returns the service's uptime percent within last 24 hours
func (s *Service) OnlineDaysPercent(days int) float32 {
	ago := utils.Now().Add((-24 * time.Duration(days)) * time.Hour)
	return s.OnlineSince(ago)
}

// OnlineSince accepts a time since parameter to return the percent of a service's uptime.
func (s *Service) OnlineSince(ago time.Time) float32 {
	failed := s.AllFailures().Since(ago)
	if len(failed) == 0 {
		s.Online24Hours = 100.00
		return s.Online24Hours
	}
	total := s.AllHits().Since(ago)
	if len(total) == 0 {
		s.Online24Hours = 0
		return s.Online24Hours
	}
	avg := float64(len(failed)) / float64(len(total)) * 100
	avg = 100 - avg
	if avg < 0 {
		avg = 0
	}
	amount, _ := strconv.ParseFloat(fmt.Sprintf("%0.2f", avg), 10)
	s.Online24Hours = float32(amount)
	return s.Online24Hours
}

// Downtime returns the amount of time of a offline service
func (s *Service) Downtime() time.Duration {
	hit := s.LastHit()
	fail := s.LastFailure()
	if hit == nil {
		return time.Duration(0)
	}
	if fail == nil {
		return utils.Now().Sub(fail.CreatedAt)
	}

	return fail.CreatedAt.Sub(hit.CreatedAt)
}