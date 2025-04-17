package handler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	registrationsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auth_registrations_total",
		Help: "Total number of successful user registrations.",
	})

	refreshesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auth_refreshes_total",
		Help: "Total number of successful token refreshes.",
	})

	interServiceTokensGeneratedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "auth_inter_service_tokens_generated_total",
		Help: "Total number of generated inter-service tokens.",
	})

	tokenVerificationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_token_verifications_total",
			Help: "Total number of token verification attempts by type and status.",
		},
		[]string{"type", "status"},
	)
)
