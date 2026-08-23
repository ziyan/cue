package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/ziyan/cue/internal/config"
)

// ruleInterval is how often every visible page is checked against its rules.
// Short enough that a session expiring is fixed before anybody notices, long
// enough that it costs nothing.
const ruleInterval = 5 * time.Second

// enforceRules keeps every open page in the state it is meant to be in: logged
// in, and free of the dialogs that appear on top of it.
//
// The rules are re-evaluated forever rather than run once when a tab opens.
// That is the whole point. The case this exists for is a camera dashboard
// whose session expires every few hours, after which the tab sits on a login
// page — showing an empty login form on a wall in an office — until somebody
// walks over with a keyboard. Logging in once when the tab opened would not
// have helped.
func (self *Browser) enforceRules(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(ruleInterval):
		}

		self.mutex.Lock()
		ready := self.ready
		tabs := make(map[string]string, len(self.tabs))
		for identifier, target := range self.tabs {
			tabs[identifier] = target
		}
		self.mutex.Unlock()

		if !ready {
			continue
		}

		for identifier, target := range tabs {
			item, found := self.itemFor(identifier)
			if !found {
				continue
			}
			if item.Login == nil && len(item.Dismiss) == 0 {
				continue
			}
			// A page that is slow to answer must not hold up the others.
			pageContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			self.applyRules(pageContext, identifier, target, item)
			cancel()
		}
	}
}

func (self *Browser) applyRules(ctx context.Context, identifier, target string, item config.Item) {
	session, err := self.session(ctx, target)
	if err != nil {
		log.Debugf("cannot reach the tab for %s: %s", describeItem(item, identifier), err)
		return
	}

	if item.Login != nil {
		needed, err := self.loginNeeded(ctx, session, item.Login)
		if err != nil {
			log.Debugf("cannot tell whether %s needs logging in: %s", describeItem(item, identifier), err)
		} else if needed {
			self.attemptLogin(ctx, identifier, item)
		}
	}

	for _, dismiss := range item.Dismiss {
		self.attemptDismiss(ctx, identifier, item, dismiss)
	}
}

// loginNeeded decides whether the page is showing a login form. Two
// independent signals, because different applications give different ones: the
// address, for the very common case of being redirected to /login, and the
// presence of an element that only the login page has, for the applications
// that log in without navigating.
func (self *Browser) loginNeeded(ctx context.Context, session *pageSession, login *config.Login) (bool, error) {
	if login.WhenURLMatches != "" {
		pattern, err := regexp.Compile(login.WhenURLMatches)
		if err != nil {
			return false, err
		}
		address, err := session.CurrentURL(ctx)
		if err != nil {
			return false, err
		}
		if pattern.MatchString(address) {
			return true, nil
		}
	}

	if login.WhenSelectorExists != "" {
		present, err := session.selectorExists(ctx, login.WhenSelectorExists)
		if err != nil {
			return false, err
		}
		if present {
			return true, nil
		}
	}

	return false, nil
}

// attemptLogin fills in the form and submits it, unless it did so too
// recently. The interval matters: a wrong password submitted in a loop is how
// an account gets locked out, and the account being locked out is much worse
// than the screen showing a login page.
func (self *Browser) attemptLogin(ctx context.Context, identifier string, item config.Item) {
	login := item.Login

	minimum := login.MinimumInterval.Duration()
	if minimum <= 0 {
		minimum = 30 * time.Second
	}

	self.mutex.Lock()
	last := self.lastLogin[identifier]
	if time.Since(last) < minimum {
		self.mutex.Unlock()
		return
	}
	self.lastLogin[identifier] = time.Now()
	self.mutex.Unlock()

	target := self.targetFor(identifier)
	session, err := self.session(ctx, target)
	if err != nil {
		log.Warningf("cannot log in to %s: %s", describeItem(item, identifier), err)
		return
	}

	log.Noticef("%s is showing a login page; signing in as %s", describeItem(item, identifier), login.Username)

	if err := session.fillAndSubmit(ctx, login); err != nil {
		log.Warningf("cannot sign in to %s: %s", describeItem(item, identifier), err)
		return
	}

	self.mutex.Lock()
	self.loginCount[identifier]++
	self.mutex.Unlock()

	if login.ExpectURLMatches == "" {
		return
	}

	// Confirm it worked, so that a wrong selector or a changed form shows up
	// in the log as "signed in but the page did not change" rather than as a
	// screen that is mysteriously still on the login page.
	pattern, err := regexp.Compile(login.ExpectURLMatches)
	if err != nil {
		return
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		address, err := session.CurrentURL(ctx)
		if err != nil {
			continue
		}
		if pattern.MatchString(address) {
			log.Noticef("%s is signed in", describeItem(item, identifier))
			return
		}
	}
	log.Warningf("%s was signed in but did not reach a page matching %q; check the selectors and the credentials",
		describeItem(item, identifier), login.ExpectURLMatches)
}

// attemptDismiss gets rid of one thing that has appeared on top of the page.
func (self *Browser) attemptDismiss(ctx context.Context, identifier string, item config.Item, dismiss config.Dismiss) {
	target := self.targetFor(identifier)
	session, err := self.session(ctx, target)
	if err != nil {
		return
	}

	acted, err := session.dismiss(ctx, dismiss)
	if err != nil {
		log.Debugf("cannot apply the dismiss rule %q on %s: %s", dismiss.Selector, describeItem(item, identifier), err)
		return
	}
	if !acted {
		return
	}

	self.mutex.Lock()
	self.dismissCount[identifier]++
	self.mutex.Unlock()

	action := "clicked"
	if dismiss.Hide {
		action = "hid"
	}
	log.Noticef("%s %q on %s", action, dismiss.Selector, describeItem(item, identifier))
}

func (self *Browser) targetFor(identifier string) string {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.tabs[identifier]
}

// --- the JavaScript ---------------------------------------------------------

// selectorExists reports whether the page has an element matching a selector
// and it is actually visible. "Present in the document" is not enough: many
// applications keep a hidden login form in the page at all times.
func (self *pageSession) selectorExists(ctx context.Context, selector string) (bool, error) {
	expression := fmt.Sprintf(`(() => {
  const element = document.querySelector(%s);
  if (!element) return false;
  const style = window.getComputedStyle(element);
  if (style.display === 'none' || style.visibility === 'hidden') return false;
  const box = element.getBoundingClientRect();
  return box.width > 0 && box.height > 0;
})()`, quote(selector))

	var present bool
	if err := self.Evaluate(ctx, expression, false, &present); err != nil {
		return false, err
	}
	return present, nil
}

// fillAndSubmit types the credentials into the form and submits it.
//
// The typing is done by setting the value and then dispatching input and
// change events. Setting .value alone is invisible to React, Vue and Angular,
// which track the value themselves and would submit an empty form — and for a
// React-controlled input even the events are not enough, because React
// installs its own value setter, so the native one has to be called
// explicitly. This is the single fiddliest thing in the project and it is
// fiddly for a reason.
func (self *pageSession) fillAndSubmit(ctx context.Context, login *config.Login) error {
	expression := fmt.Sprintf(`(() => {
  const setValue = (element, value) => {
    const prototype = Object.getPrototypeOf(element);
    const descriptor = Object.getOwnPropertyDescriptor(prototype, 'value');
    if (descriptor && descriptor.set) {
      descriptor.set.call(element, value);
    } else {
      element.value = value;
    }
    element.dispatchEvent(new Event('input', { bubbles: true }));
    element.dispatchEvent(new Event('change', { bubbles: true }));
  };

  const usernameSelector = %s;
  const username = usernameSelector ? document.querySelector(usernameSelector) : null;
  const password = document.querySelector(%s);
  if (usernameSelector && !username) return 'no username field';
  if (!password) return 'no password field';

  if (username) {
    username.focus();
    setValue(username, %s);
  }
  password.focus();
  setValue(password, %s);

  for (const selector of %s) {
    const extra = document.querySelector(selector);
    // Only click a checkbox that is not already ticked; clicking it every
    // time would toggle "remember me" off on every other sign-in.
    if (extra && !(extra.type === 'checkbox' && extra.checked) && extra.getAttribute('aria-checked') !== 'true') {
      extra.click();
    }
  }

  const submitSelector = %s;
  if (submitSelector) {
    const submit = document.querySelector(submitSelector);
    if (!submit) return 'no submit button';
    if (submit.disabled) {
      // Forms that enable the button from their own state need a moment
      // after the events above before the click will do anything.
      return 'submit button is disabled';
    }
    submit.click();
    return 'ok';
  }

  const form = password.form || username.form;
  if (form) {
    if (typeof form.requestSubmit === 'function') {
      form.requestSubmit();
    } else {
      form.submit();
    }
    return 'ok';
  }

  password.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true }));
  return 'ok';
})()`,
		quote(login.UsernameSelector),
		quote(login.PasswordSelector),
		quote(login.Username),
		quote(login.Password.Reveal()),
		quoteList(login.AlsoClick),
		quote(login.SubmitSelector))

	var outcome string
	if err := self.Evaluate(ctx, expression, false, &outcome); err != nil {
		return err
	}
	if outcome == "submit button is disabled" {
		// Try once more after the form has had time to react to the typing.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		clicked := fmt.Sprintf(`(() => {
  const submit = document.querySelector(%s);
  if (!submit) return 'no submit button';
  if (submit.disabled) return 'submit button is still disabled';
  submit.click();
  return 'ok';
})()`, quote(login.SubmitSelector))
		if err := self.Evaluate(ctx, clicked, false, &outcome); err != nil {
			return err
		}
	}
	if outcome != "ok" {
		return fmt.Errorf("browser: %s", outcome)
	}
	return nil
}

// dismiss clicks or hides one element, and reports whether it did anything, so
// that a rule which matches nothing stays silent instead of logging every five
// seconds forever.
func (self *pageSession) dismiss(ctx context.Context, rule config.Dismiss) (bool, error) {
	pattern := ""
	if rule.WhenTextMatches != "" {
		if _, err := regexp.Compile(rule.WhenTextMatches); err != nil {
			return false, err
		}
		pattern = rule.WhenTextMatches
	}

	expression := fmt.Sprintf(`(() => {
  const elements = Array.from(document.querySelectorAll(%s));
  const pattern = %s;
  const matcher = pattern ? new RegExp(pattern) : null;
  for (const element of elements) {
    const style = window.getComputedStyle(element);
    if (style.display === 'none' || style.visibility === 'hidden') continue;
    const box = element.getBoundingClientRect();
    if (box.width <= 0 || box.height <= 0) continue;
    if (matcher && !matcher.test(element.textContent || '')) continue;
    if (%t) {
      element.style.setProperty('display', 'none', 'important');
    } else {
      element.click();
    }
    return true;
  }
  return false;
})()`, quote(rule.Selector), quote(pattern), rule.Hide)

	var acted bool
	if err := self.Evaluate(ctx, expression, false, &acted); err != nil {
		return false, err
	}
	return acted, nil
}

// quoteList renders a list of strings as a JavaScript array literal.
func quoteList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// quote renders a Go string as a JavaScript string literal. encoding/json
// produces exactly that, and it escapes the characters — quotes, backslashes,
// line separators — that a credential or a selector might contain.
func quote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		// Marshalling a string cannot fail; an empty literal is a safe
		// fallback that makes the rule do nothing rather than break the page.
		return `""`
	}
	return string(encoded)
}

// LoginCount and DismissCount are what the interface shows. The useful
// question about a display is not "is it up" but "how often does it have to be
// rescued", and these are half the answer.
func (self *Browser) LoginCount(identifier string) int {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.loginCount[identifier]
}

func (self *Browser) DismissCount(identifier string) int {
	self.mutex.Lock()
	defer self.mutex.Unlock()
	return self.dismissCount[identifier]
}
