import { useCallback, useRef, useState } from "react";
import type { Room, LocalTrack } from "../../sdk/src";

/**
 * Compartilhamento de tela via getDisplayMedia() + SDK (room.publishScreen()).
 * Publica a faixa de tela no SFU e volta ao normal quando o usuario para.
 */
export function useScreenShare(room: Room | null) {
  const [sharing, setSharing] = useState(false);
  const [screenStream, setScreenStream] = useState<MediaStream | null>(null);
  const publishedRef = useRef<{ video?: LocalTrack; audio?: LocalTrack } | null>(null);
  const streamRef = useRef<MediaStream | null>(null);

  const stop = useCallback(async () => {
    const s = streamRef.current;
    if (s) {
      for (const t of s.getTracks()) t.stop();
    }
    const pub = publishedRef.current;
    if (room && pub) {
      try {
        if (pub.video) await room.unpublish(pub.video);
        if (pub.audio) await room.unpublish(pub.audio);
      } catch {
        // ignora falhas ao despublicar
      }
    }
    publishedRef.current = null;
    streamRef.current = null;
    setScreenStream(null);
    setSharing(false);
  }, [room]);

  const start = useCallback(async () => {
    if (!room) return;
    try {
      const bundle = await room.publishScreen();
      publishedRef.current = { video: bundle.video, audio: bundle.audio };
      streamRef.current = bundle.stream;
      setScreenStream(bundle.stream);
      setSharing(true);

      // quando o usuario clica em "Parar de compartilhar" no dialog nativo do navegador
      const vt = bundle.stream.getVideoTracks()[0];
      if (vt) {
        vt.addEventListener("ended", () => {
          void stop();
        });
      }
    } catch (err) {
      // usuario cancelou a selecao de tela — nao e um erro real
      setSharing(false);
    }
  }, [room, stop]);

  const toggle = useCallback(() => {
    if (sharing) void stop();
    else void start();
  }, [sharing, start, stop]);

  return { sharing, screenStream, start, stop, toggle };
}
