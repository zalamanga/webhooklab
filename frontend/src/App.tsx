"use client";

import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, api, errorMessage, setCsrfToken } from "./api";
import AuditPage from "./AuditPage";
import { ErrorBox, Loading } from "./components";
import DeliveriesPage from "./DeliveriesPage";
import EventPage from "./EventPage";
import ReceiversPage from "./ReceiversPage";
import SendPage, { newEventDraft } from "./SendPage";
import type { Session } from "./types";

function useHashRoute() {
  const [hash, setHash] = useState(
    window.location.hash.slice(1) || "/deliveries",
  );
  useEffect(() => {
    function change() {
      setHash(window.location.hash.slice(1) || "/deliveries");
      window.scrollTo({ top: 0 });
    }
    window.addEventListener("hashchange", change);
    return () => window.removeEventListener("hashchange", change);
  }, []);
  const [path, search] = hash.split("?");
  return {
    path,
    segments: path.split("/").filter(Boolean),
    query: new URLSearchParams(search),
  };
}

export default function App() {
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);
  return mounted ? (
    <Workspace />
  ) : (
    <div className="boot-screen">
      <div className="wordmark">WebhookLab</div>
      <Loading>Opening the delivery workspace...</Loading>
    </div>
  );
}

function Workspace() {
  const route = useHashRoute();
  const [session, setSession] = useState<Session | null>(null);
  const [checking, setChecking] = useState(true);
  const [sessionError, setSessionError] = useState<string | null>(null);
  const [expired, setExpired] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const [draft, setDraft] = useState(newEventDraft);
  const [notice, setNotice] = useState<string | null>(null);
  const logoutRequest = useRef(false);
  async function checkSession() {
    setChecking(true);
    setSessionError(null);
    try {
      const current = await api<Session>("/auth/session", {}, false);
      setCsrfToken(current.csrfToken);
      setSession(current);
    } catch (cause) {
      if (!(cause instanceof ApiError && cause.status === 401))
        setSessionError(errorMessage(cause));
    } finally {
      setChecking(false);
    }
  }
  useEffect(() => {
    void checkSession();
  }, []);
  useEffect(() => {
    function expire() {
      setSession(null);
      setCsrfToken("");
      setExpired(true);
      setNotice(null);
    }
    window.addEventListener("webhooklab:session-expired", expire);
    return () =>
      window.removeEventListener("webhooklab:session-expired", expire);
  }, []);
  useEffect(() => {
    const names: Record<string, string> = {
      deliveries: "Deliveries",
      send: "Send an event",
      events: "Delivery evidence",
      receivers: "Demo receivers",
      audit: "Audit history",
    };
    document.title = `${names[route.segments[0]] || "Deliveries"} | WebhookLab`;
  }, [route.path]);
  async function logout() {
    if (logoutRequest.current) return;
    logoutRequest.current = true;
    setLoggingOut(true);
    setLogoutError(null);
    try {
      await api<void>("/auth/logout", { method: "POST" });
      setSession(null);
      setCsrfToken("");
      setExpired(false);
      setNotice(null);
    } catch (cause) {
      setLogoutError(errorMessage(cause));
    } finally {
      logoutRequest.current = false;
      setLoggingOut(false);
    }
  }
  if (checking)
    return (
      <div className="boot-screen">
        <div className="wordmark">WebhookLab</div>
        <Loading>Checking your session...</Loading>
      </div>
    );
  if (!session)
    return (
      <Login
        expired={expired}
        sessionError={sessionError}
        onRetry={checkSession}
        onLogin={(current) => {
          setSession(current);
          setCsrfToken(current.csrfToken);
          setExpired(false);
          setSessionError(null);
          setLogoutError(null);
        }}
      />
    );
  const active = ["send", "receivers", "audit"].includes(route.segments[0])
    ? route.segments[0]
    : "deliveries";
  return (
    <div className="app-shell">
      <a
        className="skip-link"
        href="#main-content"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById("main-content")?.focus();
        }}
      >
        Skip to main content
      </a>
      <header className="app-header">
        <div className="header-inner">
          <a
            className="wordmark"
            href="#/deliveries"
            aria-label="WebhookLab deliveries"
          >
            WebhookLab<span>Local delivery laboratory</span>
          </a>
          <nav className="main-nav" aria-label="Workspace">
            {[
              ["deliveries", "Deliveries"],
              ["send", "Send event"],
              ["receivers", "Demo receivers"],
              ["audit", "Audit"],
            ].map(([path, label]) => (
              <a
                key={path}
                href={`#/${path}`}
                className={active === path ? "active" : ""}
                aria-current={active === path ? "page" : undefined}
              >
                {label}
              </a>
            ))}
          </nav>
          <div className="account">
            <span>{session.user.name}</span>
            <button
              className="button text-button"
              disabled={loggingOut}
              onClick={logout}
            >
              {loggingOut ? "Signing out..." : "Sign out"}
            </button>
          </div>
        </div>
      </header>
      <main id="main-content" tabIndex={-1}>
        {logoutError && <ErrorBox message={logoutError} onRetry={logout} />}
        {notice && (
          <div className="notice success global-notice" role="status">
            <p>{notice}</p>
            <button
              className="button text-button"
              onClick={() => setNotice(null)}
            >
              Dismiss
            </button>
          </div>
        )}
        {active === "send" ? (
          <SendPage
            user={session.user}
            draft={draft}
            setDraft={setDraft}
            onCreated={(delivery) => {
              setNotice(
                "Event recorded. Open the attempt sequence below to follow its delivery evidence.",
              );
              window.location.hash = `/events/${delivery.id}`;
            }}
          />
        ) : active === "receivers" ? (
          <ReceiversPage />
        ) : active === "audit" ? (
          <AuditPage query={route.query} />
        ) : route.segments[0] === "events" && route.segments.length === 2 ? (
          <EventPage key={route.segments[1]} eventId={route.segments[1]} />
        ) : (
          <DeliveriesPage query={route.query} />
        )}
      </main>
      <footer className="app-footer">
        <span>WebhookLab · Synthetic events and local receivers</span>
        <span>
          Sender attempts and receiver receipts are separate evidence.
        </span>
      </footer>
    </div>
  );
}

function Login({
  expired,
  sessionError,
  onRetry,
  onLogin,
}: {
  expired: boolean;
  sessionError: string | null;
  onRetry: () => void;
  onLogin: (session: Session) => void;
}) {
  const [email, setEmail] = useState("operator@webhooklab.test");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inFlight = useRef(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    try {
      onLogin(
        await api<Session>(
          "/auth/login",
          {
            method: "POST",
            body: JSON.stringify({ email: email.trim(), password }),
          },
          false,
        ),
      );
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  return (
    <div className="login-page">
      <header className="login-header">
        <div className="wordmark">
          WebhookLab<span>Local delivery laboratory</span>
        </div>
      </header>
      <main className="login-layout">
        <div className="login-context">
          <h1>
            Inspect delivery.
            <br />
            Keep the evidence.
          </h1>
          <p>
            A synthetic webhook workspace for following durable events, worker
            attempts and local receiver receipts.
          </p>
          <ol className="login-sequence">
            <li>
              <span>01</span>
              <div>
                <strong>Record an event</strong>
                <p>Store the payload with its queued delivery.</p>
              </div>
            </li>
            <li>
              <span>02</span>
              <div>
                <strong>Read each attempt</strong>
                <p>Inspect status, timing and bounded response text.</p>
              </div>
            </li>
            <li>
              <span>03</span>
              <div>
                <strong>Compare the receipt</strong>
                <p>
                  Distinguish sender uncertainty from local receiver evidence.
                </p>
              </div>
            </li>
          </ol>
        </div>
        <section className="sheet login-form" aria-labelledby="login-heading">
          <h2 id="login-heading">Sign in to WebhookLab</h2>
          <p className="muted">
            Use the local operator account created during setup.
          </p>
          {expired && (
            <div className="notice" role="status">
              <strong>Your session expired</strong>
              <p>
                Sign in again to continue. An uncertain event draft keeps its
                original values and retry identity in this tab.
              </p>
            </div>
          )}
          {sessionError && (
            <ErrorBox message={sessionError} onRetry={onRetry} />
          )}
          <form className="stack-form" onSubmit={submit}>
            <div className="field">
              <label htmlFor="email">Email address</label>
              <input
                id="email"
                type="email"
                required
                autoComplete="username"
                disabled={busy}
                value={email}
                onChange={(event) => setEmail(event.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="password">Password</label>
              <input
                id="password"
                type="password"
                required
                autoComplete="current-password"
                disabled={busy}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </div>
            {error && (
              <div className="notice error" role="alert">
                <p>{error}</p>
              </div>
            )}
            <button className="button primary" disabled={busy}>
              {busy ? "Signing in..." : "Sign in"}
            </button>
          </form>
          <p className="login-password-help">
            The setup script generates the local password. It is never prefilled
            here.
          </p>
        </section>
      </main>
      <footer className="login-footer">
        A local laboratory with fixed demo destinations. No business-order state
        or real payments are changed.
      </footer>
    </div>
  );
}
