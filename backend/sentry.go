package main

import "github.com/getsentry/sentry-go"

func sentryOptions(c configuration) sentry.ClientOptions {
	traceSampleRate := 0.1
	if c.DevelopmentSignup {
		traceSampleRate = 1
	}
	return sentry.ClientOptions{
		Dsn: c.SentryDSN, Environment: c.SentryEnvironment, Release: c.SentryRelease,
		EnableTracing: true, TracesSampleRate: traceSampleRate,
		AttachStacktrace: true,
		DataCollection: &sentry.DataCollection{
			UserInfo: sentry.Set(false), HTTPBodies: []sentry.BodyType{},
			Cookies:     &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			QueryParams: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			HTTPHeaders: &sentry.HeaderCollectionConfig{
				Request:  &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
				Response: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			},
		},
		BeforeSend: redactSentryEvent, BeforeSendTransaction: redactSentryEvent,
	}
}

func redactSentryEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	// Raw URLs, panic values and local variables can contain WhatsApp or account secrets.
	event.Request, event.Breadcrumbs = nil, nil
	event.User, event.ServerName = sentry.User{}, ""
	if event.Type != "transaction" {
		event.Transaction = ""
		if event.Message != "" {
			event.Message = "Application error (details omitted for privacy)"
		}
	} else if event.TransactionInfo == nil || event.TransactionInfo.Source != sentry.SourceRoute {
		event.Transaction = "unmatched route"
	}
	for i := range event.Exception {
		event.Exception[i].Value = "Application error (details omitted for privacy)"
		if stack := event.Exception[i].Stacktrace; stack != nil {
			for j := range stack.Frames {
				stack.Frames[j].Vars = nil
			}
		}
	}
	for i := range event.Threads {
		if stack := event.Threads[i].Stacktrace; stack != nil {
			for j := range stack.Frames {
				stack.Frames[j].Vars = nil
			}
		}
	}
	return event
}
