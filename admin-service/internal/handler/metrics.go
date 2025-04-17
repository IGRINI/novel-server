package handler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	userBansTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_user_bans_total",
		Help: "Total number of successful user bans.",
	})
	userUnbansTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_user_unbans_total",
		Help: "Total number of successful user unbans.",
	})
	passwordResetsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_password_resets_total",
		Help: "Total number of successful password resets.",
	})
	userUpdatesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "admin_user_updates_total",
		Help: "Total number of successful user updates.",
	})
)
