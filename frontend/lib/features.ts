// Feature gates. Set PEEKS_UI to false to cut peeks (M5b): the card is hidden and maxPeeks is not sent.
export const PEEKS_UI = true;

// Google sign-in on /login and /signup. Off until the OAuth client exists (D-0.6):
// the button, its "or" divider and the role_required retry are hidden, and the
// GSI script is never loaded. Email and password sign-in is unaffected.
// The backend still accepts POST /auth/google; only the UI is gated.
export const GOOGLE_AUTH = false;
