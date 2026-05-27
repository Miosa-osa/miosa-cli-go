// Package miosa provides a Go client for the MIOSA public API.
//
// # Quick start
//
//	client := miosa.NewClient("msk_u_...")
//
//	ctx := context.Background()
//
//	computer, err := client.Computers.Create(ctx, miosa.CreateComputerInput{
//	    Name: "my-agent",
//	    Size: miosa.SizeSmall,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	_ = computer.Start(ctx)
//	_ = computer.Wait(ctx, miosa.StatusRunning)
//
//	png, _ := computer.Screenshot(ctx)
//	_ = computer.Click(ctx, 100, 200)
//	_ = computer.Type(ctx, "hello world")
//	_ = computer.Key(ctx, "Return")
//
//	result, _ := computer.Bash(ctx, "ls -la /home")
//	fmt.Println(result.Output)
//
//	session, _ := computer.Agent.Run(ctx, miosa.RunAgentInput{
//	    Goal: "Open Firefox and search for MIOSA",
//	})
//	events, _ := computer.Agent.Stream(ctx, session.ID)
//	for ev := range events {
//	    fmt.Println(ev.Type, ev.Data)
//	}
//
//	_ = computer.Destroy(ctx)
//
// # Authentication
//
// All requests are authenticated with an API key of the form msk_u_<key>.
// Obtain your key at https://miosa.ai/settings/api.
//
// # Error handling
//
// Every error returned by this SDK is one of:
//   - *MiosaError (base)
//   - *AuthenticationError (401)
//   - *InsufficientCreditsError (402)
//   - *PermissionError (403)
//   - *NotFoundError (404)
//   - *ValidationError (422)
//   - *RateLimitError (429)
//   - *ServerError (5xx)
//   - *ConnectionError (transport level)
//
// Use errors.As to inspect typed errors:
//
//	var notFound *miosa.NotFoundError
//	if errors.As(err, &notFound) {
//	    // handle missing resource
//	}
//
// # Retries
//
// The client automatically retries 429 and 5xx responses with exponential
// backoff and full jitter. The default is 3 retries. Override with
// WithMaxRetries(0) to disable.
package miosa
