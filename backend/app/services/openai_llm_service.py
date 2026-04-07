from __future__ import annotations

import logging
import time

from openai import OpenAI

from app.config import OpenAIConfig


class OpenaiLLMService:
    """
    Language model service using the official OpenAI Python SDK.
    """

    def __init__(self, config: OpenAIConfig) -> None:
        self.api_key = config.api_key
        self.default_model = config.model
        self.max_output_tokens = config.max_output_tokens
        self.temperature = config.temperature
        self.client = OpenAI(api_key=self.api_key)
        self._logger = logging.getLogger("miko.backend.llm.openai")

    def generate_response(self, messages: list[dict], session_id: str = "") -> str:
        if not self.api_key.strip():
            raise RuntimeError("openai api key is missing")
        started = time.perf_counter()
        self._logger.info(
            "request start session_id=%s model=%s messages=%d",
            session_id or "-",
            self.default_model,
            len(messages),
        )

        try:
            response = self.client.chat.completions.create(
                model=self.default_model,
                messages=messages,
                max_tokens=self.max_output_tokens,
                temperature=self.temperature,
            )
        except Exception:
            self._logger.exception(
                "request failed session_id=%s after=%sms",
                session_id or "-",
                round((time.perf_counter() - started) * 1000),
            )
            raise
        content = response.choices[0].message.content if response.choices else ""
        text = (content or "").strip()
        self._logger.info(
            "request done session_id=%s after=%sms chars=%d",
            session_id or "-",
            round((time.perf_counter() - started) * 1000),
            len(text),
        )
        return text
