package main

import (
	"flag"
	"net/http"
	"os"
	"strings"

	"github.com/ViBiOh/flags"
	"github.com/ViBiOh/httputils/v4/pkg/alcotest"
	"github.com/ViBiOh/httputils/v4/pkg/cors"
	"github.com/ViBiOh/httputils/v4/pkg/health"
	"github.com/ViBiOh/httputils/v4/pkg/httperror"
	"github.com/ViBiOh/httputils/v4/pkg/httputils"
	"github.com/ViBiOh/httputils/v4/pkg/logger"
	"github.com/ViBiOh/httputils/v4/pkg/owasp"
	"github.com/ViBiOh/httputils/v4/pkg/prometheus"
	"github.com/ViBiOh/httputils/v4/pkg/recoverer"
	"github.com/ViBiOh/httputils/v4/pkg/server"
	"github.com/ViBiOh/mailer/pkg/client"
	mailer "github.com/ViBiOh/mailer/pkg/client"
	"github.com/ViBiOh/notifier/pkg/alertmanager"
	"github.com/ViBiOh/notifier/pkg/flux"
	"github.com/ViBiOh/notifier/pkg/grafana"
	"github.com/ViBiOh/notifier/pkg/ssh"
)

const (
	alertmanagerPath = "/alertmanager"
	fluxPath         = "/flux"
	sshPath          = "/ssh"
)

func main() {
	fs := flag.NewFlagSet("notifier", flag.ExitOnError)

	appServerConfig := server.Flags(fs, "")
	promServerConfig := server.Flags(fs, "prometheus", flags.NewOverride("Port", 9090), flags.NewOverride("IdleTimeout", "10s"), flags.NewOverride("ShutdownTimeout", "5s"))
	healthConfig := health.Flags(fs, "")

	alcotestConfig := alcotest.Flags(fs, "")
	loggerConfig := logger.Flags(fs, "logger")
	prometheusConfig := prometheus.Flags(fs, "prometheus", flags.NewOverride("Gzip", false))
	owaspConfig := owasp.Flags(fs, "")
	corsConfig := cors.Flags(fs, "cors")

	sshConfig := ssh.Flags(fs, "ssh")

	alertmanagerConfig := alertmanager.Flags(fs, "alertmanager")
	grafanaConfig := grafana.Flags(fs, "grafana")
	mailerConfig := mailer.Flags(fs, "mailer")

	logger.Fatal(fs.Parse(os.Args[1:]))

	alcotest.DoAndExit(alcotestConfig)
	logger.Global(logger.New(loggerConfig))
	defer logger.Close()

	appServer := server.New(appServerConfig)
	promServer := server.New(promServerConfig)
	prometheusApp := prometheus.New(prometheusConfig)
	healthApp := health.New(healthConfig)

	grafanaApp := grafana.New(grafanaConfig)

	mailerClient, err := client.New(mailerConfig, prometheusApp.Registerer())
	logger.Fatal(err)
	defer mailerClient.Close()

	alertmanagerApp := http.StripPrefix(alertmanagerPath, alertmanager.New(alertmanagerConfig, mailerClient).Handler())
	fluxHandler := http.StripPrefix(fluxPath, flux.New(grafanaApp).Handler())
	sshHandler := http.StripPrefix(sshPath, ssh.New(sshConfig, mailerClient).Handler())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, alertmanagerPath) {
			alertmanagerApp.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, fluxPath) {
			fluxHandler.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, sshPath) {
			sshHandler.ServeHTTP(w, r)
			return
		}

		httperror.NotFound(w)
	})

	go promServer.Start("prometheus", healthApp.End(), prometheusApp.Handler())
	go appServer.Start("http", healthApp.End(), httputils.Handler(handler, healthApp, recoverer.Middleware, prometheusApp.Middleware, owasp.New(owaspConfig).Middleware, cors.New(corsConfig).Middleware))

	healthApp.WaitForTermination(appServer.Done())
	server.GracefulWait(appServer.Done(), promServer.Done())
}
