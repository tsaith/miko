from __future__ import annotations

import logging

from .llm_service import LLMService


class BrainService:
    """
    Conversational state manager and LLM coordinator.
    Maintains per-session history and keeps the system prompt stable.
    """

    def __init__(self, llm: LLMService) -> None:
        self.llm = llm
        self._logger = logging.getLogger("miko.backend.brain")
        self.system_prompt = """
你是一位名叫 Miko 的桌上型語音智能助理。
你會像真人助理一樣和使用者自然交談，不要說自己是 AI 或語言模型。
請主要使用繁體中文回覆，語氣自然、簡潔、有親和力。
你的文字會直接交給 TTS 唸出，所以請避免使用不適合口語的格式、清單符號或舞台動作描述。
如果使用者問題不明確，優先用一句簡短問題確認。
""".strip()
        self.history = [{"role": "system", "content": self.system_prompt}]

    def reset(self) -> None:
        self.history = [{"role": "system", "content": self.system_prompt}]

    def process_message(self, text: str, session_id: str = "") -> str:
        user_text = (text or "").strip()
        if not user_text:
            return ""

        self.history.append({"role": "user", "content": user_text})
        if len(self.history) > 11:
            self.history = [self.history[0]] + self.history[-10:]
        self._logger.info(
            "process start session_id=%s history=%d chars=%d",
            session_id or "-",
            len(self.history),
            len(user_text),
        )

        reply = (self.llm.generate_response(self.history, session_id=session_id) or "").strip()
        if not reply:
            reply = "抱歉，我剛剛沒有整理好回覆，請再說一次。"

        self.history.append({"role": "assistant", "content": reply})
        self._logger.info(
            "process done session_id=%s history=%d reply_chars=%d",
            session_id or "-",
            len(self.history),
            len(reply),
        )
        return reply
