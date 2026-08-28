// Whether leaving the page now would throw work away.
//
// A page with a form registers a check here; the shell asks it before it lets
// a click on the sidebar change the page, and the browser asks it before a
// reload or a closed tab. Kept in a module of its own rather than in app.js
// because the pages would then have to import the thing that imports them.

let check = null;

// warnBeforeLeaving registers a predicate that reports whether there is
// unsaved work. A page that registers one must forget it on the way out.
export function warnBeforeLeaving(predicate) {
  check = predicate;
}

export function forgetWarning() {
  check = null;
}

export function wouldLoseWork() {
  try {
    return typeof check === "function" && check() === true;
  } catch (error) {
    // A broken check must not make the interface impossible to leave.
    return false;
  }
}

export const leavingMessage =
  "There are changes on this page that have not been saved. Leave anyway?";
