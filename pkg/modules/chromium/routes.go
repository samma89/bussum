package chromium

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
)

// Bounds on the scoped extra HTTP headers feature. Chromium matches every
// scoped header against every paused sub-resource request, so the total
// matching work is the product of the header count and the sub-resource count.
// These caps bound the factors the client controls; [scopeMatchBudget] bounds
// the product. See https://github.com/gotenberg/gotenberg/issues/1588.
const (
	maxExtraHttpHeaders           = 64
	maxExtraHttpHeaderScopeLength = 1024

	// A scope pattern matches against a URL, which takes microseconds for any
	// reasonable pattern.
	extraHttpHeaderScopeMatchTimeout = 250 * time.Millisecond
)

var sameSiteRegexp = regexp2.MustCompile(
	`("sameSite"\s*:\s*")(?i:(lax|strict|none))(")`,
	regexp2.None,
)

// FormDataChromiumOptions creates [Options] from the form data.
//
// It falls back to the default value if the considered key is not present.
//
// JSON-encoded fields:
//   - failOnHttpStatusCodes: []int
//   - failOnResourceHttpStatusCodes: []int
//   - ignoreResourceHttpStatusDomains: []string
//   - failOnConsoleMessageTypes: []string
//   - cookies: []Cookie
//   - extraHttpHeaders: map[string]string
//   - emulatedMediaFeatures: map[string]string
//
// Domain filtering only applies to resource checks triggered by
// "failOnResourceHttpStatusCodes".
func FormDataChromiumOptions(ctx *api.Context) (*api.FormData, Options) {
	defaultOptions := DefaultOptions()

	var (
		skipNetworkIdleEvent            bool
		skipNetworkAlmostIdleEvent      bool
		failOnHttpStatusCodes           []int64
		failOnResourceHttpStatusCodes   []int64
		ignoreResourceHttpStatusDomains []string
		failOnResourceLoadingFailed     bool
		failOnConsoleExceptions         bool
		failOnConsoleMessageTypes       []string
		waitDelay                       time.Duration
		waitWindowStatus                string
		waitForExpression               string
		waitForSelector                 string
		cookies                         []Cookie
		userAgent                       string
		extraHttpHeaders                []ExtraHttpHeader
		emulatedMediaType               string
		emulatedMediaFeatures           []EmulatedMediaFeature
		omitBackground                  bool
	)

	form := ctx.FormData().
		Bool("skipNetworkIdleEvent", &skipNetworkIdleEvent, defaultOptions.SkipNetworkIdleEvent).
		Bool("skipNetworkAlmostIdleEvent", &skipNetworkAlmostIdleEvent, defaultOptions.SkipNetworkAlmostIdleEvent).
		Custom("failOnHttpStatusCodes", func(value string) error {
			if value == "" {
				failOnHttpStatusCodes = defaultOptions.FailOnHttpStatusCodes
				return nil
			}

			err := json.Unmarshal([]byte(value), &failOnHttpStatusCodes)
			if err != nil {
				return fmt.Errorf("unmarshal failOnHttpStatusCodes: %w", err)
			}

			return nil
		}).
		Custom("failOnResourceHttpStatusCodes", func(value string) error {
			if value == "" {
				failOnResourceHttpStatusCodes = defaultOptions.FailOnResourceHttpStatusCodes
				return nil
			}

			err := json.Unmarshal([]byte(value), &failOnResourceHttpStatusCodes)
			if err != nil {
				return fmt.Errorf("unmarshal failOnResourceHttpStatusCodes: %w", err)
			}

			return nil
		}).
		Custom("ignoreResourceHttpStatusDomains", func(value string) error {
			if value == "" {
				ignoreResourceHttpStatusDomains = defaultOptions.IgnoreResourceHttpStatusDomains
				return nil
			}

			err := json.Unmarshal([]byte(value), &ignoreResourceHttpStatusDomains)
			if err != nil {
				return fmt.Errorf("unmarshal ignoreResourceHttpStatusDomains: %w", err)
			}

			return nil
		}).
		Bool("failOnResourceLoadingFailed", &failOnResourceLoadingFailed, defaultOptions.FailOnResourceLoadingFailed).
		Bool("failOnConsoleExceptions", &failOnConsoleExceptions, defaultOptions.FailOnConsoleExceptions).
		Custom("failOnConsoleMessageTypes", func(value string) error {
			if value == "" {
				failOnConsoleMessageTypes = defaultOptions.FailOnConsoleMessageTypes
				return nil
			}

			err := json.Unmarshal([]byte(value), &failOnConsoleMessageTypes)
			if err != nil {
				return fmt.Errorf("unmarshal failOnConsoleMessageTypes: %w", err)
			}

			for _, messageType := range failOnConsoleMessageTypes {
				if !slices.Contains(consoleMessageTypes, messageType) {
					return fmt.Errorf("unknown console message type '%s', expected one or more of: %s", messageType, strings.Join(consoleMessageTypes, ", "))
				}
			}

			return nil
		}).
		Duration("waitDelay", &waitDelay, defaultOptions.WaitDelay).
		String("waitWindowStatus", &waitWindowStatus, defaultOptions.WaitWindowStatus).
		String("waitForExpression", &waitForExpression, defaultOptions.WaitForExpression).
		String("waitForSelector", &waitForSelector, defaultOptions.WaitForSelector).
		Custom("cookies", func(value string) error {
			if value == "" {
				cookies = defaultOptions.Cookies
				return nil
			}

			// sameSite attribute from cookies must accept case-insensitive
			// values.
			// See https://github.com/gotenberg/gotenberg/issues/1331.
			normalized, err := sameSiteRegexp.ReplaceFunc(value, func(m regexp2.Match) string {
				groups := m.Groups()
				provided := groups[2].String()
				var canon string
				switch strings.ToLower(provided) {
				case "lax":
					canon = "Lax"
				case "strict":
					canon = "Strict"
				case "none":
					canon = "None"
				default:
					canon = provided
				}
				return groups[1].String() + canon + groups[3].String()
			}, -1, -1)
			if err != nil {
				return fmt.Errorf("normalize sameSite from cookies: %w", err)
			}

			err = json.Unmarshal([]byte(normalized), &cookies)
			if err != nil {
				return fmt.Errorf("unmarshal cookies: %w", err)
			}

			for i, cookie := range cookies {
				if strings.TrimSpace(cookie.Name) == "" || strings.TrimSpace(cookie.Value) == "" || strings.TrimSpace(cookie.Domain) == "" {
					err = errors.Join(err, fmt.Errorf("cookie %d must have its name, value and domain set", i))
				}
			}

			return err
		}).
		String("userAgent", &userAgent, defaultOptions.UserAgent).
		Custom("extraHttpHeaders", func(value string) error {
			if value == "" {
				extraHttpHeaders = defaultOptions.ExtraHttpHeaders
				return nil
			}

			var headers map[string]string
			err := json.Unmarshal([]byte(value), &headers)
			if err != nil {
				return fmt.Errorf("unmarshal extraHttpHeaders: %w", err)
			}

			if len(headers) > maxExtraHttpHeaders {
				return fmt.Errorf("too many headers, got %d, expected at most %d", len(headers), maxExtraHttpHeaders)
			}

			for k, v := range headers {
				var scope string
				var valueTokens []string
				var invalidScopeToken bool

				tokens := strings.SplitSeq(v, ";")
				for token := range tokens {
					if strings.HasPrefix(strings.ToLower(strings.TrimSpace(token)), "scope") {
						tokenNoSpaces := strings.Join(strings.Fields(token), "")
						parts := strings.SplitN(tokenNoSpaces, "=", 2)

						if len(parts) == 2 && strings.ToLower(parts[0]) == "scope" && parts[1] != "" {
							scope = parts[1]
						} else {
							err = errors.Join(err, fmt.Errorf("invalid scope '%s' for header '%s'", scope, k))
							invalidScopeToken = true
							break
						}
					} else if token != "" {
						valueTokens = append(valueTokens, token)
					}
				}

				if invalidScopeToken {
					continue
				}

				var scopeRegexp *regexp2.Regexp
				if len(scope) > 0 {
					if len(scope) > maxExtraHttpHeaderScopeLength {
						err = errors.Join(err, fmt.Errorf("scope regex pattern for header '%s' is too long, got %d characters, expected at most %d", k, len(scope), maxExtraHttpHeaderScopeLength))
						continue
					}

					p, errCompile := regexp2.Compile(scope, regexp2.None)
					if errCompile != nil {
						err = errors.Join(err, fmt.Errorf("invalid scope regex pattern for header '%s': %w", k, errCompile))
						continue
					}
					p.MatchTimeout = extraHttpHeaderScopeMatchTimeout
					scopeRegexp = p
				}

				extraHttpHeaders = append(extraHttpHeaders, ExtraHttpHeader{
					Name:  k,
					Value: strings.Join(valueTokens, "; "),
					Scope: scopeRegexp,
				})
			}

			return err
		}).
		Custom("emulatedMediaType", func(value string) error {
			if value == "" {
				emulatedMediaType = defaultOptions.EmulatedMediaType
				return nil
			}

			if value != "screen" && value != "print" {
				return errors.New("wrong value, expected either 'screen', 'print' or empty")
			}

			emulatedMediaType = value

			return nil
		}).
		Custom("emulatedMediaFeatures", func(value string) error {
			if value == "" {
				emulatedMediaFeatures = defaultOptions.EmulatedMediaFeatures
				return nil
			}

			var features map[string]string
			err := json.Unmarshal([]byte(value), &features)
			if err != nil {
				return fmt.Errorf("unmarshal emulatedMediaFeatures: %w", err)
			}

			for k, v := range features {
				emulatedMediaFeatures = append(emulatedMediaFeatures, EmulatedMediaFeature{
					Name:  k,
					Value: v,
				})
			}

			return err
		}).
		Bool("omitBackground", &omitBackground, defaultOptions.OmitBackground)

	options := Options{
		SkipNetworkIdleEvent:            skipNetworkIdleEvent,
		SkipNetworkAlmostIdleEvent:      skipNetworkAlmostIdleEvent,
		FailOnHttpStatusCodes:           failOnHttpStatusCodes,
		FailOnResourceHttpStatusCodes:   failOnResourceHttpStatusCodes,
		IgnoreResourceHttpStatusDomains: ignoreResourceHttpStatusDomains,
		FailOnResourceLoadingFailed:     failOnResourceLoadingFailed,
		FailOnConsoleExceptions:         failOnConsoleExceptions,
		FailOnConsoleMessageTypes:       failOnConsoleMessageTypes,
		WaitDelay:                       waitDelay,
		WaitWindowStatus:                waitWindowStatus,
		WaitForExpression:               waitForExpression,
		WaitForSelector:                 waitForSelector,
		Cookies:                         cookies,
		UserAgent:                       userAgent,
		ExtraHttpHeaders:                extraHttpHeaders,
		EmulatedMediaType:               emulatedMediaType,
		EmulatedMediaFeatures:           emulatedMediaFeatures,
		OmitBackground:                  omitBackground,
	}

	return form, options
}

// HandleError maps a Chromium conversion error to a client-facing HTTP error.
//
// The Markdown URL route uses it so a bad wait expression or a crashed
// browser has a client-facing status. A nil error stays nil. An error this
// function does not recognize is returned unchanged.
func HandleError(err error, options Options) error {
	return handleChromiumError(err, options)
}

func handleChromiumError(err error, options Options) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, ErrChromiumCrashed) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusServiceUnavailable,
				"Chromium crashed while processing the request. Retry, or reduce the workload if the problem persists.",
			),
		)
	}

	if errors.Is(err, ErrInvalidEvaluationExpression) {
		if options.WaitForExpression == "" {
			// We do not expect the 'waitWindowStatus' form field to return
			// an ErrInvalidEvaluationExpression error. In such a scenario,
			// we return a 500.
			return err
		}

		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusBadRequest,
				fmt.Sprintf("The expression '%s' (waitForExpression) returned an exception or undefined", options.WaitForExpression),
			),
		)
	}

	if errors.Is(err, ErrInvalidSelectorQuery) {
		if options.WaitForSelector == "" {
			// We only expect to see this error if the user specified a selector.
			// If they didn't and we still generated the error, return a 500.
			return err
		}

		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusBadRequest,
				fmt.Sprintf("The selector '%s' (waitForSelector) returned an exception or undefined", options.WaitForSelector),
			),
		)
	}

	if errors.Is(err, ErrInvalidHttpStatusCode) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusConflict,
				fmt.Sprintf("Invalid HTTP status code from the main page: %s", strings.ReplaceAll(err.Error(), fmt.Sprintf(": %s", ErrInvalidHttpStatusCode.Error()), "")),
			),
		)
	}

	if errors.Is(err, ErrInvalidResourceHttpStatusCode) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusConflict,
				fmt.Sprintf("Invalid HTTP status code from resources:\n%s", strings.ReplaceAll(err.Error(), fmt.Sprintf(": %s", ErrInvalidResourceHttpStatusCode.Error()), "")),
			),
		)
	}

	if errors.Is(err, ErrConsoleExceptions) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusConflict,
				fmt.Sprintf("Chromium console exceptions:\n%s", strings.ReplaceAll(err.Error(), ErrConsoleExceptions.Error(), "")),
			),
		)
	}

	if errors.Is(err, ErrConsoleMessages) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusConflict,
				fmt.Sprintf("Chromium console messages:\n%s", strings.TrimSuffix(err.Error(), fmt.Sprintf(": %s", ErrConsoleMessages.Error()))),
			),
		)
	}

	if errors.Is(err, ErrLoadingFailed) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusBadRequest,
				fmt.Sprintf("Chromium returned %v", err),
			),
		)
	}

	if errors.Is(err, ErrResourceLoadingFailed) {
		return api.WrapError(
			err,
			api.NewSentinelHttpError(
				http.StatusConflict,
				fmt.Sprintf("Chromium failed to load resources: %v", strings.ReplaceAll(err.Error(), fmt.Sprintf(": %s", ErrResourceLoadingFailed.Error()), "")),
			),
		)
	}

	return err
}
