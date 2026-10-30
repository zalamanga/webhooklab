import { useCallback, useEffect, useRef, useState } from "react";
import { api, errorMessage } from "./api";

export function useResource<T>(path: string, poll = false) {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  const replace = useCallback((value: T) => {
    generation.current += 1;
    setData(value);
    setError(null);
    setUpdatedAt(new Date().toISOString());
  }, []);

  useEffect(() => {
    let disposed = false;
    let inFlight = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    setData(null);
    setError(null);
    setLoading(true);
    setUpdatedAt(null);

    function schedule() {
      if (!disposed && poll && document.visibilityState === "visible")
        timer = setTimeout(() => {
          void load(false);
        }, 2000);
    }
    async function load(initial: boolean) {
      if (
        disposed ||
        inFlight ||
        (!initial && document.visibilityState !== "visible")
      )
        return;
      inFlight = true;
      controller = new AbortController();
      setRefreshing(true);
      const requestGeneration = generation.current;
      try {
        const result = await api<T>(path, { signal: controller.signal });
        if (
          !disposed &&
          !controller.signal.aborted &&
          requestGeneration === generation.current
        ) {
          setData(result);
          setError(null);
          setUpdatedAt(new Date().toISOString());
        }
      } catch (cause) {
        if (
          !disposed &&
          !controller.signal.aborted &&
          requestGeneration === generation.current
        )
          setError(errorMessage(cause));
      } finally {
        inFlight = false;
        if (!disposed) {
          setLoading(false);
          setRefreshing(false);
          schedule();
        }
      }
    }
    function visibilityChanged() {
      if (timer) clearTimeout(timer);
      if (document.visibilityState === "visible") {
        if (!inFlight) void load(false);
      } else controller?.abort();
    }
    void load(true);
    if (poll) document.addEventListener("visibilitychange", visibilityChanged);
    return () => {
      disposed = true;
      controller?.abort();
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", visibilityChanged);
    };
  }, [path, poll, revision]);

  return {
    data,
    loading,
    refreshing,
    error,
    updatedAt,
    replace,
    refresh: () => {
      setLoading(true);
      setRevision((value) => value + 1);
    },
  };
}
