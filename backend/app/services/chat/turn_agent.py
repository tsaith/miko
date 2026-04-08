from __future__ import annotations

from dataclasses import dataclass
import logging
import time

from app.config import OpenAIConfig, TurnAgentConfig
from app.services.openai_llm_service import OpenaiLLMService


@dataclass(slots=True)
class TurnDecision:
    is_user_speak_end: bool
    source: str
    raw: str
    user_text: str
    decided_at: float
    mode: str = "final"


class TurnAgent:
    def __init__(self, openai: OpenAIConfig, config: TurnAgentConfig) -> None:
        self._config = config
        self._logger = logging.getLogger("miko.backend.chat.turn_agent")
        self._llm = OpenaiLLMService(
            OpenAIConfig(
                api_key=openai.api_key,
                model=config.model,
                max_output_tokens=config.max_output_tokens,
                temperature=config.temperature,
            )
        )
        self.reset()

    def reset(self) -> None:
        self._assistant_text = ""
        self._user_text = ""
        self._latest_fragment = ""
        self._latest_is_final = False
        self._dirty = False
        self._last_decision: TurnDecision | None = None
        self._last_preview_decision: TurnDecision | None = None

    def push_assistant_text(self, text: str) -> None:
        self._assistant_text = (text or "").strip()

    def push_user_text(self, text: str, is_final: bool = False) -> None:
        fragment = (text or "").strip()
        if not fragment:
            return
        merged = self._merge_user_text(self._user_text, fragment)
        if merged != self._user_text or fragment != self._latest_fragment or is_final != self._latest_is_final:
            self._user_text = merged
            self._latest_fragment = fragment
            self._latest_is_final = is_final
            self._dirty = True

    def current_user_text(self) -> str:
        return self._user_text

    def consume_user_text(self) -> str:
        text = self._user_text
        self._user_text = ""
        self._latest_fragment = ""
        self._latest_is_final = False
        self._dirty = False
        self._last_decision = None
        self._last_preview_decision = None
        return text

    def last_decision(self) -> TurnDecision | None:
        return self._last_decision

    def last_preview_decision(self) -> TurnDecision | None:
        return self._last_preview_decision

    def preview_user_speak_end(self) -> TurnDecision:
        user_text = self._user_text.strip()
        if not user_text:
            decision = TurnDecision(
                is_user_speak_end=False,
                source="preview-empty",
                raw="CONTINUE",
                user_text="",
                decided_at=time.monotonic(),
                mode="partial",
            )
            self._last_preview_decision = decision
            return decision

        heuristic = self._heuristic_decision(user_text, mode="partial")
        if heuristic is not None:
            self._last_preview_decision = heuristic
            return heuristic

        decision = TurnDecision(
            is_user_speak_end=False,
            source="preview-conservative",
            raw="CONTINUE",
            user_text=user_text,
            decided_at=time.monotonic(),
            mode="partial",
        )
        self._last_preview_decision = decision
        return decision

    def is_user_speak_end(self) -> bool:
        if not self._dirty and self._last_decision is not None:
            return self._last_decision.is_user_speak_end

        user_text = self._user_text.strip()
        if not user_text:
            self._last_decision = TurnDecision(
                is_user_speak_end=False,
                source="empty",
                raw="EMPTY",
                user_text="",
                decided_at=time.monotonic(),
            )
            self._dirty = False
            return False

        heuristic = self._heuristic_decision(user_text)
        if heuristic is not None:
            self._last_decision = heuristic
            self._dirty = False
            self._logger.info(
                "decision mode=%s source=%s result=%s chars=%d",
                heuristic.mode,
                heuristic.source,
                heuristic.is_user_speak_end,
                len(user_text),
            )
            return heuristic.is_user_speak_end

        finalized = self._finalized_stt_decision(user_text)
        if finalized is not None:
            self._last_decision = finalized
            self._dirty = False
            self._logger.info(
                "decision mode=%s source=%s result=%s chars=%d",
                finalized.mode,
                finalized.source,
                finalized.is_user_speak_end,
                len(user_text),
            )
            return finalized.is_user_speak_end

        if not self._config.enabled or not self._llm.api_key.strip():
            fallback = self._fallback_decision(user_text)
            self._last_decision = fallback
            self._dirty = False
            self._logger.info(
                "decision mode=%s source=%s result=%s chars=%d",
                fallback.mode,
                fallback.source,
                fallback.is_user_speak_end,
                len(user_text),
            )
            return fallback.is_user_speak_end

        try:
            raw = self._llm.generate_response(self._build_messages(user_text))
        except Exception as exc:
            self._logger.warning("decision llm failed err=%s", exc)
            fallback = self._fallback_decision(user_text)
            self._last_decision = fallback
            self._dirty = False
            return fallback.is_user_speak_end

        label = raw.strip().upper()
        decision = TurnDecision(
            is_user_speak_end=label == "END",
            source="llm",
            raw=raw.strip(),
            user_text=user_text,
            decided_at=time.monotonic(),
            mode="final",
        )
        self._last_decision = decision
        self._dirty = False
        self._logger.info(
            "decision mode=%s source=llm result=%s raw=%s chars=%d",
            decision.mode,
            decision.is_user_speak_end,
            decision.raw,
            len(user_text),
        )
        return decision.is_user_speak_end

    def _build_messages(self, user_text: str) -> list[dict[str, str]]:
        return [
            {
                "role": "system",
                "content": (
                    "You are a turn-taking classifier for a spoken conversation between a user and a desktop voice assistant. "
                    "Decide whether the user's current utterance is complete enough that the assistant should start replying now. "
                    "Be conservative. If the user may still continue after a short pause, answer CONTINUE. "
                    "Return only one token: END or CONTINUE."
                ),
            },
            {
                "role": "user",
                "content": (
                    f"Latest assistant text:\n{self._assistant_text or '<none>'}\n\n"
                    f"Accumulated user text:\n{user_text}\n\n"
                    f"Latest transcript fragment:\n{self._latest_fragment or '<none>'}\n\n"
                    f"Latest fragment is final from STT: {'yes' if self._latest_is_final else 'no'}"
                ),
            },
        ]

    def _heuristic_decision(self, user_text: str, mode: str = "final") -> TurnDecision | None:
        stripped = user_text.strip()
        if len(stripped) < 2:
            return TurnDecision(False, f"{mode}-heuristic-short", "CONTINUE", stripped, time.monotonic(), mode=mode)

        continue_suffixes = ("然後", "所以", "因為", "如果", "但是", "而且", "就是", "還有", "例如", "比如")
        if stripped.endswith(continue_suffixes):
            return TurnDecision(False, f"{mode}-heuristic-continue", "CONTINUE", stripped, time.monotonic(), mode=mode)

        if mode == "partial":
            if len(stripped) < 8:
                return TurnDecision(
                    False,
                    "partial-heuristic-too-short",
                    "CONTINUE",
                    stripped,
                    time.monotonic(),
                    mode=mode,
                )
            if stripped.endswith(("，", "、", ",", "...", "…")):
                return TurnDecision(
                    False,
                    "partial-heuristic-open-tail",
                    "CONTINUE",
                    stripped,
                    time.monotonic(),
                    mode=mode,
                )
            return None

        if stripped.endswith(("。", "！", "？", ".", "!", "?")):
            return TurnDecision(True, "heuristic-punctuation", "END", stripped, time.monotonic(), mode=mode)
        return None

    def _fallback_decision(self, user_text: str) -> TurnDecision:
        stripped = user_text.strip()
        terminal_tokens = ("嗎", "呢", "吧", "是不是", "對嗎", "好嗎")
        if stripped.endswith(terminal_tokens) or len(stripped) >= 24:
            return TurnDecision(True, "fallback-length", "END", stripped, time.monotonic(), mode="final")
        return TurnDecision(False, "fallback-conservative", "CONTINUE", stripped, time.monotonic(), mode="final")

    def _finalized_stt_decision(self, user_text: str) -> TurnDecision | None:
        if not self._latest_is_final:
            return None

        stripped = user_text.strip()
        if not stripped:
            return TurnDecision(False, "finalized-empty", "CONTINUE", stripped, time.monotonic(), mode="final")

        if self._looks_unfinished(stripped):
            return TurnDecision(False, "finalized-open-tail", "CONTINUE", stripped, time.monotonic(), mode="final")

        return TurnDecision(True, "finalized-stt", "END", stripped, time.monotonic(), mode="final")

    def _looks_unfinished(self, text: str) -> bool:
        stripped = text.strip()
        if not stripped:
            return False
        if stripped.endswith(("，", "、", ",", "...", "…")):
            return True
        return stripped.endswith(("然後", "所以", "因為", "如果", "但是", "而且", "就是", "還有", "例如", "比如"))

    def _merge_user_text(self, current: str, incoming: str) -> str:
        if not current:
            return incoming
        if incoming.startswith(current):
            return incoming
        if current.startswith(incoming):
            return current
        if current.endswith(incoming):
            return current
        separator = "" if current.endswith(("，", "。", "！", "？", "、", ",", ".", "!", "?")) else " "
        return f"{current}{separator}{incoming}".strip()
