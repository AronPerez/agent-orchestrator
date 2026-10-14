"use client";

import Image from "next/image";
import { useEffect, useRef, useState } from "react";

const MUX_PLAYBACK_ID =
	process.env.NEXT_PUBLIC_MUX_PLAYBACK_ID ??
	"cpmHxjRygocH1rPeKq6jk4UYxGghl8B8ABcop4Gc01b8";
const VIDEO_TITLE = "AO Demo";
const VIDEO_SOURCE = `https://stream.mux.com/${MUX_PLAYBACK_ID}.m3u8`;

function PlayIcon({ className = "" }: { className?: string }) {
	return (
		<svg
			className={className}
			viewBox="0 0 24 24"
			fill="currentColor"
			aria-hidden="true"
		>
			<path d="M8 5.5v13a1 1 0 0 0 1.53.85l10.2-6.5a1 1 0 0 0 0-1.7L9.53 4.65A1 1 0 0 0 8 5.5Z" />
		</svg>
	);
}

export function VideoSection() {
	const [playing, setPlaying] = useState(false);
	const [playbackError, setPlaybackError] = useState(false);
	const videoRef = useRef<HTMLVideoElement>(null);

	useEffect(() => {
		const video = videoRef.current;
		if (!playing || !video) return;
		if (video.canPlayType("application/vnd.apple.mpegurl")) {
			video.src = VIDEO_SOURCE;
			return;
		}

		// Load only the HLS playback engine, without the vendor analytics client.
		let disposed = false;
		let destroy: (() => void) | undefined;
		void import("hls.js").then(({ default: Hls }) => {
			if (disposed) return;
			if (!Hls.isSupported()) {
				setPlaybackError(true);
				return;
			}
			const hls = new Hls();
			destroy = () => hls.destroy();
			hls.on(Hls.Events.ERROR, (_event, data) => {
				if (!disposed && data.fatal) setPlaybackError(true);
			});
			hls.loadSource(VIDEO_SOURCE);
			hls.attachMedia(video);
		}).catch(() => {
			if (!disposed) setPlaybackError(true);
		});
		return () => {
			disposed = true;
			destroy?.();
		};
	}, [playing]);

	return (
		<section id="see-it" className="relative px-4 py-16 sm:px-8 sm:py-20 lg:px-[30px] lg:py-24">
			<div className="mx-auto max-w-7xl">
				<div className="max-w-3xl text-left select-none">
					<h2 className="text-2xl sm:text-3xl lg:text-4xl font-semibold text-foreground">
						See it in action
					</h2>
					<p className="mt-3 text-base text-muted-foreground">
						Watch AO run a fleet of agents end to end on a single repo, from task to merged PR.
					</p>
				</div>

				<div className="relative mx-auto mt-12 w-full">
					<div
						data-testid="video-frame"
						className="relative aspect-video overflow-hidden bg-black"
					>
						{playing ? (
							<video
								ref={videoRef}
								controls
								autoPlay
								playsInline
								aria-label={VIDEO_TITLE}
								className="absolute inset-0 h-full w-full"
								onError={() => setPlaybackError(true)}
							/>
						) : (
							<button
								type="button"
								onClick={() => setPlaying(true)}
								aria-label={`Play video: ${VIDEO_TITLE}`}
								className="group absolute inset-0 cursor-pointer"
							>
								<Image
									src="/mux-video-preview.jpg"
									alt="Still from the Agent Orchestrator demo video"
									fill
									sizes="(min-width: 1280px) 1280px, 100vw"
									className="object-cover"
								/>
								<span className="absolute inset-0 grid place-items-center">
									<PlayIcon className="h-14 w-14 translate-x-[3px] text-white transition-transform duration-200 group-hover:scale-110 sm:h-16 sm:w-16" />
								</span>
							</button>
						)}
					</div>
					{playbackError && (
						<p role="alert" className="mt-3 text-sm text-muted-foreground">
							Unable to play the demo video. Please reload and try again.
						</p>
					)}
				</div>
			</div>
		</section>
	);
}
