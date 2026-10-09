import { createContext, type ReactNode, useContext, useEffect, useState } from "react";
import { useLocation } from "react-router-dom";
import useCurrentUser from "@/hooks/useCurrentUser";
import type { JournalWanderSession } from "@/lib/journal";
import { ROUTES } from "@/router/routes";

interface JournalSessionContextValue {
  reviewDay: { date: string; timezone: string } | undefined;
  setReviewDay: (day: { date: string; timezone: string } | undefined) => void;
  wander: JournalWanderSession | undefined;
  setWander: (session: JournalWanderSession | undefined) => void;
  expanded: ReadonlySet<string>;
  setExpanded: (name: string, expanded: boolean) => void;
}

const JournalSessionContext = createContext<JournalSessionContextValue | undefined>(undefined);

function JournalSession({ children }: { children: ReactNode }) {
  const { pathname } = useLocation();
  const [reviewDay, setReviewDay] = useState<{ date: string; timezone: string }>();
  const [wander, setWander] = useState<JournalWanderSession>();
  const [expanded, setExpandedNames] = useState<ReadonlySet<string>>(new Set());
  useEffect(() => {
    // A detail detour retains the reading session. Leaving the reader lets a
    // later entry use its new local day/timezone without moving an open page.
    if (pathname.startsWith("/memos/")) return;
    if (pathname !== ROUTES.JOURNAL_REVIEW) setReviewDay(undefined);
    if (pathname !== ROUTES.JOURNAL_WANDER) setWander(undefined);
  }, [pathname]);
  return (
    <JournalSessionContext.Provider
      value={{
        reviewDay,
        setReviewDay,
        wander,
        setWander,
        expanded,
        setExpanded: (name, value) =>
          setExpandedNames((previous) => {
            const next = new Set(previous);
            if (value) next.add(name);
            else next.delete(name);
            return next;
          }),
      }}
    >
      {children}
    </JournalSessionContext.Provider>
  );
}

/** History lives only in this signed-in session, surviving a trip to a memo detail. */
export function JournalSessionProvider({ children }: { children: ReactNode }) {
  const user = useCurrentUser();
  return <JournalSession key={user?.name ?? "guest"}>{children}</JournalSession>;
}

export function useJournalSession() {
  const value = useContext(JournalSessionContext);
  if (!value) throw new Error("JournalSessionProvider is required");
  return value;
}
