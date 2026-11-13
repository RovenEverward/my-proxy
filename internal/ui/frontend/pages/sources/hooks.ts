import { useCallback, useEffect, useRef, useState } from "react";
import { api, requestEvents } from "../../runtime/api";
import { errorMessage, sourcePath, type SubscriptionSource } from "../../domain/routing";

export function useSourceRuntime(initial: SubscriptionSource, onSettled: () => Promise<void>) {
  const [source, setSource] = useState(initial);
  const [error, setError] = useState("");
  const [reading, setReading] = useState(false);
  const mounted = useRef(false);
  const generation = useRef(0);
  const currentSource = useRef(source); currentSource.current = source;
  const settled = useRef(onSettled); settled.current = onSettled;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; generation.current++; }; }, []);
  const refresh = useCallback(async () => {
    const ownGeneration = ++generation.current;
    setReading(true);
    try {
      const current = await api<SubscriptionSource>(sourcePath(initial.id));
      if (!mounted.current || generation.current !== ownGeneration) return;
      const wasRefreshing = currentSource.current.refreshing;
      setSource(current); setError("");
      if (wasRefreshing && !current.refreshing) await settled.current();
    } catch (reason) { if (mounted.current && generation.current === ownGeneration) setError(errorMessage(reason)); }
    finally { if (mounted.current && generation.current === ownGeneration) setReading(false); }
  }, [initial.id]);
  useEffect(() => {
    if (!source.refreshing || error) return;
    const timer = setTimeout(() => { if (!document.hidden) void refresh(); else setSource(previous => ({ ...previous })); }, 1000);
    return () => clearTimeout(timer);
  }, [source, error, refresh]);
  return { source, error, reading, refresh };
}
export function useWrittenRevision() {
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    const changed = () => setRevision(previous => previous + 1);
    requestEvents.addEventListener("written", changed);
    return () => requestEvents.removeEventListener("written", changed);
  }, []);
  return revision;
}
