package fakeprovidercli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/zigai/aht/v2/pkg/fakeprovider"
)

const (
	eventListening = "listening"
	eventRequest   = "request"
	pollInterval   = 100 * time.Millisecond
	portBits       = 16
)

var errInvalidAddress = errors.New("invalid listen address")

type serveListening struct {
	Event string `json:"event"`
	URL   string `json:"url"`
}

type serveRequest struct {
	Event    string                `json:"event"`
	Index    int                   `json:"index"`
	Kind     fakeprovider.Kind     `json:"kind"`
	Protocol fakeprovider.Protocol `json:"protocol"`
	Path     string                `json:"path"`
	UserText string                `json:"user_text"`
}

func (app *application) newServeCommand() *cobra.Command {
	var address string

	command := &cobra.Command{
		Use:   "serve",
		Short: "Serve the fake model provider in the foreground",
		Long: "Serve the Echo fake model provider until Ctrl-C. Each model " +
			"request is printed when it arrives.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateAddress(address); err != nil {
				return err
			}

			return app.runServe(cmd.Context(), address)
		},
	}
	command.Flags().
		StringVar(&address, "address", "", "listen address `<host:port>` [default: 127.0.0.1:0]")

	return command
}

func (app *application) runServe(ctx context.Context, address string) error {
	//nolint:contextcheck // The provider serves until Close, so its lifetime is not bound to the command context.
	server, err := fakeprovider.Start(fakeprovider.Options{
		Address:     address,
		Model:       "",
		Script:      fakeprovider.Echo(),
		Hold:        false,
		SideMarkers: nil,
	})
	if err != nil {
		return fmt.Errorf("start fake provider: %w", err)
	}

	if err := app.writeListening(server.URL()); err != nil {
		return finish(err, server.Close())
	}

	return finish(app.streamRequests(ctx, server), server.Close())
}

func (app *application) streamRequests(ctx context.Context, server *fakeprovider.Server) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	printedConversation, printedSide := 0, 0

	for {
		conversation := server.Requests()
		side := server.SideRequests()
		pending := slices.Concat(conversation[printedConversation:], side[printedSide:])
		slices.SortStableFunc(pending, func(a, b fakeprovider.Request) int {
			return a.ReceivedAt.Compare(b.ReceivedAt)
		})

		for _, request := range pending {
			if err := app.writeRequest(request); err != nil {
				return err
			}
		}

		printedConversation, printedSide = len(conversation), len(side)

		select {
		case <-ctx.Done():
			return fmt.Errorf("stop serving requests: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (app *application) writeListening(url string) error {
	if app.outputJSON {
		return app.writeJSONLine(serveListening{Event: eventListening, URL: url})
	}

	return app.writeDetails([]detail{{label: "URL", value: url}})
}

func (app *application) writeRequest(request fakeprovider.Request) error {
	if app.outputJSON {
		return app.writeJSONLine(serveRequest{
			Event:    eventRequest,
			Index:    request.Index,
			Kind:     request.Kind,
			Protocol: request.Protocol,
			Path:     request.Path,
			UserText: request.UserText,
		})
	}

	return app.writef(
		"%s #%d %s %s %q\n",
		request.Kind,
		request.Index,
		request.Protocol,
		request.Path,
		request.UserText,
	)
}

func validateAddress(address string) error {
	if address == "" {
		return nil
	}

	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return usageError{err: fmt.Errorf("%w %q: want HOST:PORT", errInvalidAddress, address)}
	}

	if _, err := strconv.ParseUint(port, 10, portBits); err != nil {
		return usageError{
			err: fmt.Errorf("%w %q: port must be from 0 to 65535", errInvalidAddress, address),
		}
	}

	return nil
}
