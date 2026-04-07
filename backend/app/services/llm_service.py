from __future__ import annotations

from app.config import OpenAIConfig

from .openai_llm_service import OpenaiLLMService


class LLMService:
    """
    Router for LLM requests.
    The project currently supports OpenAI only, but keeps the router shape
    from the reference project.
    """

    def __init__(self, config: OpenAIConfig, engine: str = "openai") -> None:
        self.engine = engine
        self._openai = OpenaiLLMService(config)

    def _get_engine(self) -> OpenaiLLMService:
        if self.engine != "openai":
            raise ValueError(f"unknown llm engine: {self.engine}")
        return self._openai

    def generate_response(self, messages: list[dict], session_id: str = "") -> str:
        return self._get_engine().generate_response(messages, session_id=session_id)
