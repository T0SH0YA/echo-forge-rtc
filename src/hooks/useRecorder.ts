import { useCallback, useRef, useState } from "react";

/**
 * Gravacao da reuniao no lado do cliente.
 *
 * Usa getDisplayMedia() para capturar a aba/tela da reuniao (todos os participantes
 * como aparecem na tela) e mistura o microfone local. O resultado e salvo como .webm
 * no dispositivo do usuario. Nao envia nada para servidores externos.
 *
 * @param micStream stream local (para incluir a voz do proprio usuario no arquivo)
 * @param onBlob callback chamado quando a gravacao termina (para pedir confirmacao de download)
 */
export function useRecorder(
  micStream: MediaStream | null,
  onBlob: (blob: Blob, suggestedName: string) => void,
) {
  const [recording, setRecording] = useState(false);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const captureStreamRef = useRef<MediaStream | null>(null);
  const ctxRef = useRef<AudioContext | null>(null);

  const cleanup = useCallback(() => {
    const cs = captureStreamRef.current;
    if (cs) for (const t of cs.getTracks()) t.stop();
    captureStreamRef.current = null;
    if (ctxRef.current) {
      void ctxRef.current.close().catch(() => {});
      ctxRef.current = null;
    }
    recorderRef.current = null;
  }, []);

  const start = useCallback(async () => {
    if (recording) return;
    try {
      const display = await navigator.mediaDevices.getDisplayMedia({
        video: { frameRate: 30 },
        audio: true,
      });

      // mistura o audio da tela/aba com o microfone local
      const AC = window.AudioContext || (window as any).webkitAudioContext;
      const ctx: AudioContext = new AC();
      ctxRef.current = ctx;
      const dest = ctx.createMediaStreamDestination();

      if (display.getAudioTracks().length > 0) {
        ctx.createMediaStreamSource(new MediaStream(display.getAudioTracks())).connect(dest);
      }
      if (micStream && micStream.getAudioTracks().length > 0) {
        ctx.createMediaStreamSource(new MediaStream(micStream.getAudioTracks())).connect(dest);
      }

      const mixed = new MediaStream([
        ...display.getVideoTracks(),
        ...dest.stream.getAudioTracks(),
      ]);
      captureStreamRef.current = display;

      const mime = MediaRecorder.isTypeSupported("video/webm;codecs=vp9,opus")
        ? "video/webm;codecs=vp9,opus"
        : "video/webm";
      const rec = new MediaRecorder(mixed, { mimeType: mime });
      chunksRef.current = [];
      rec.ondataavailable = (e) => {
        if (e.data && e.data.size > 0) chunksRef.current.push(e.data);
      };
      rec.onstop = () => {
        const blob = new Blob(chunksRef.current, { type: "video/webm" });
        const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
        onBlob(blob, `teli-reuniao-${stamp}.webm`);
        cleanup();
        setRecording(false);
      };

      // se o usuario parar a captura pelo dialog nativo, encerra a gravacao
      const vt = display.getVideoTracks()[0];
      if (vt) vt.addEventListener("ended", () => rec.state !== "inactive" && rec.stop());

      recorderRef.current = rec;
      rec.start(1000);
      setRecording(true);
    } catch {
      // usuario cancelou a selecao de tela
      cleanup();
      setRecording(false);
    }
  }, [recording, micStream, onBlob, cleanup]);

  const stop = useCallback(() => {
    const rec = recorderRef.current;
    if (rec && rec.state !== "inactive") rec.stop();
  }, []);

  const toggle = useCallback(() => {
    if (recording) stop();
    else void start();
  }, [recording, start, stop]);

  return { recording, start, stop, toggle };
}
