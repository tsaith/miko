"""
exp_turn_agent.py
=================
手動實驗腳本，驗證 TurnAgent 在各種情境下的決策行為。

執行方式（從 backend/ 目錄）：
    uv run python -m experiments.exp_turn_agent

若有設定 OPENAI_API_KEY 環境變數，也會執行真實 LLM 路徑測試。
"""
from __future__ import annotations

import logging
import os
import sys
import textwrap
from dataclasses import dataclass
from typing import Any

# ── 確保 backend 根目錄在 path 上 ────────────────────────────────────────────
sys.path.insert(0, str(__import__("pathlib").Path(__file__).parent.parent))

from app.config import OpenAIConfig, TurnAgentConfig
from app.services.chat.turn_agent import TurnAgent, TurnDecision

# ── Logger ───────────────────────────────────────────────────────────────────
logging.basicConfig(
    level=logging.WARNING,
    format="%(levelname)s %(name)s: %(message)s",
)


# ─────────────────────────────────────────────────────────────────────────────
# Helper utilities
# ─────────────────────────────────────────────────────────────────────────────

PASS = "\033[32mPASS\033[0m"
FAIL = "\033[31mFAIL\033[0m"
SKIP = "\033[33mSKIP\033[0m"
BOLD = "\033[1m"
RESET = "\033[0m"


@dataclass
class TestCase:
    name: str
    user_text: str
    is_final: bool
    assistant_text: str
    expected_end: bool
    expected_source_prefix: str  # 比對 source 的前綴，空字串表示不驗證
    description: str = ""
    skip: bool = False


@dataclass
class PreviewTestCase:
    name: str
    user_text: str
    expected_end: bool
    expected_source_prefix: str
    description: str = ""


class ExperimentRunner:
    def __init__(self, api_key: str = "") -> None:
        self._api_key = api_key
        self._passed = 0
        self._failed = 0
        self._skipped = 0

    # ── Factory helpers ───────────────────────────────────────────────────────

    def _make_agent(self, *, enabled: bool = True, api_key: str | None = None) -> TurnAgent:
        """建立一個 TurnAgent；api_key=None 表示使用實驗配置的 key。"""
        key = api_key if api_key is not None else self._api_key
        openai_cfg = OpenAIConfig(api_key=key)
        turn_cfg = TurnAgentConfig(enabled=enabled, model="gpt-4o-mini", max_output_tokens=16, temperature=0.0)
        return TurnAgent(openai=openai_cfg, config=turn_cfg)

    # ── Result reporting ──────────────────────────────────────────────────────

    def _report(self, name: str, ok: bool, decision: TurnDecision | None, note: str = "") -> None:
        status = PASS if ok else FAIL
        if decision:
            detail = f"end={decision.is_user_speak_end} source={decision.source!r} raw={decision.raw!r}"
        else:
            detail = "(no decision)"
        note_str = f"  ← {note}" if note else ""
        print(f"  {status}  {name}{note_str}")
        if not ok:
            print(f"       {detail}")

    def _check(
        self,
        name: str,
        decision: TurnDecision,
        expected_end: bool,
        expected_source_prefix: str,
        skip: bool = False,
    ) -> None:
        if skip:
            self._skipped += 1
            print(f"  {SKIP}  {name}")
            return
        end_ok = decision.is_user_speak_end == expected_end
        src_ok = (not expected_source_prefix) or decision.source.startswith(expected_source_prefix)
        ok = end_ok and src_ok
        if ok:
            self._passed += 1
        else:
            self._failed += 1
        note = ""
        if not end_ok:
            note += f"expected end={expected_end} got {decision.is_user_speak_end}; "
        if not src_ok:
            note += f"expected source prefix={expected_source_prefix!r} got {decision.source!r}"
        self._report(name, ok, decision, note.strip())

    # ─────────────────────────────────────────────────────────────────────────
    # Test suites
    # ─────────────────────────────────────────────────────────────────────────

    def run_heuristic_suite(self) -> None:
        """測試 _heuristic_decision 能直接決策的各種情形（不需 LLM）。"""
        print(f"\n{BOLD}[1] Heuristic 決策（不觸發 LLM）{RESET}")

        cases: list[TestCase] = [
            # ── 空白輸入 ──────────────────────────────────────────────────
            TestCase(
                name="空字串",
                user_text="",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="empty",
                description="空字串應直接回傳 False",
            ),
            TestCase(
                name="只有空白",
                user_text="   ",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="empty",
            ),
            # ── 太短 ──────────────────────────────────────────────────────
            TestCase(
                name="單字（短於2字）",
                user_text="好",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="final-heuristic-short",
            ),
            # ── continue 後綴（中文連接詞）────────────────────────────────
            TestCase(
                name="然後（continue suffix）",
                user_text="我想說然後",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="final-heuristic-continue",
            ),
            TestCase(
                name="所以（continue suffix）",
                user_text="他很忙所以",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="final-heuristic-continue",
            ),
            TestCase(
                name="因為（continue suffix）",
                user_text="我去了因為",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="final-heuristic-continue",
            ),
            TestCase(
                name="但是（continue suffix）",
                user_text="我想去但是",
                is_final=False,
                assistant_text="",
                expected_end=False,
                expected_source_prefix="final-heuristic-continue",
            ),
            # ── 終止標點符號 ──────────────────────────────────────────────
            TestCase(
                name="中文句號結尾",
                user_text="今天天氣不錯。",
                is_final=False,
                assistant_text="",
                expected_end=True,
                expected_source_prefix="heuristic-punctuation",
            ),
            TestCase(
                name="中文問號結尾",
                user_text="你好嗎？",
                is_final=False,
                assistant_text="",
                expected_end=True,
                expected_source_prefix="heuristic-punctuation",
            ),
            TestCase(
                name="中文驚嘆號結尾",
                user_text="太棒了！",
                is_final=False,
                assistant_text="",
                expected_end=True,
                expected_source_prefix="heuristic-punctuation",
            ),
            TestCase(
                name="英文句號結尾",
                user_text="I am done.",
                is_final=False,
                assistant_text="",
                expected_end=True,
                expected_source_prefix="heuristic-punctuation",
            ),
            TestCase(
                name="英文問號結尾",
                user_text="Are you there?",
                is_final=False,
                assistant_text="",
                expected_end=True,
                expected_source_prefix="heuristic-punctuation",
            ),
            TestCase(
                name="英文驚嘆號結尾",
                user_text="That's great!",
                is_final=False,
                assistant_text="",
                expected_end=True,
                expected_source_prefix="heuristic-punctuation",
            ),
        ]

        agent = self._make_agent(enabled=False, api_key="")  # 關閉 LLM，純 heuristic
        for tc in cases:
            if tc.skip:
                self._skipped += 1
                print(f"  {SKIP}  {tc.name}")
                continue
            agent.reset()
            agent.push_assistant_text(tc.assistant_text)
            agent.push_user_text(tc.user_text, is_final=tc.is_final)
            result = agent.is_user_speak_end()
            decision = agent.last_decision()
            assert decision is not None, "last_decision() 不應為 None"
            self._check(tc.name, decision, tc.expected_end, tc.expected_source_prefix)

    def run_finalized_stt_suite(self) -> None:
        """測試 STT is_final=True 時的 finalized-stt 路徑。"""
        print(f"\n{BOLD}[2] Finalized STT 決策（is_final=True）{RESET}")

        agent = self._make_agent(enabled=False, api_key="")

        # finalized-stt: 正常完整句子（非 open tail）→ END
        agent.reset()
        agent.push_user_text("好的我明白了", is_final=True)
        agent.is_user_speak_end()
        d = agent.last_decision()
        assert d
        self._check("完整句子 is_final=True → END", d, True, "finalized-stt")

        # finalized-open-tail: 以中文逗號結尾 → CONTINUE
        agent.reset()
        agent.push_user_text("我想說，", is_final=True)
        agent.is_user_speak_end()
        d = agent.last_decision()
        assert d
        self._check("中文逗號結尾 is_final=True → CONTINUE", d, False, "finalized-open-tail")

        # finalized-open-tail: 以省略號結尾 → CONTINUE
        agent.reset()
        agent.push_user_text("可是…", is_final=True)
        agent.is_user_speak_end()
        d = agent.last_decision()
        assert d
        self._check("省略號結尾 is_final=True → CONTINUE", d, False, "finalized-open-tail")

        # finalized-open-tail: 以 continue-suffix 結尾 → CONTINUE
        agent.reset()
        agent.push_user_text("我去了然後", is_final=True)
        agent.is_user_speak_end()
        d = agent.last_decision()
        assert d
        # 注意：heuristic 也會攔截這個，優先於 finalized-stt
        self._check("continue suffix is_final=True → CONTINUE", d, False, "")

        # is_final=False 時不觸發 finalized-stt 路徑 → fallback
        agent.reset()
        agent.push_user_text("好的我明白了", is_final=False)
        agent.is_user_speak_end()
        d = agent.last_decision()
        assert d
        self._check("is_final=False 不觸發 finalized-stt", d, False, "fallback")  # fallback-conservative（< 24 chars）

    def run_fallback_suite(self) -> None:
        """測試 LLM disabled / no API key 時的 fallback 路徑。"""
        print(f"\n{BOLD}[3] Fallback 決策（LLM disabled）{RESET}")

        agent = self._make_agent(enabled=False, api_key="")

        fallback_cases = [
            # 短文字，無終止 token → CONTINUE (fallback-conservative)
            ("短文字無終止 token → CONTINUE", "明天", False, "fallback-conservative"),
            # 以「嗎」結尾 → END (fallback-length)
            ("以「嗎」結尾 → END", "你去嗎", False, "fallback-length"),
            # 以「呢」結尾 → END
            ("以「呢」結尾 → END", "那你呢", False, "fallback-length"),
            # 以「吧」結尾 → END
            ("以「吧」結尾 → END", "好吧", False, "fallback-length"),
            # 以「是不是」結尾 → END
            ("以「是不是」結尾 → END", "你是不是", False, "fallback-length"),
            # 長度 >= 24 字 → END (fallback-length)
            # 注意：文字中不能含有 continue suffix（然後/所以…），否則 heuristic 會先攔截
            ("長度 >= 24 個字 → END", "我今天早上起床吃了早餐接著去上班走了很久真的很累", False, "fallback-length"),
        ]

        for name, text, is_final, expected_src in fallback_cases:
            agent.reset()
            agent.push_user_text(text, is_final=is_final)
            result = agent.is_user_speak_end()
            d = agent.last_decision()
            assert d
            expected_end = expected_src == "fallback-length"
            self._check(name, d, expected_end, expected_src)

    def run_preview_suite(self) -> None:
        """測試 preview_user_speak_end 的行為（partial mode）。"""
        print(f"\n{BOLD}[4] Preview 決策（partial mode）{RESET}")

        agent = self._make_agent(enabled=False, api_key="")

        preview_cases: list[PreviewTestCase] = [
            PreviewTestCase(
                name="空文字 preview → CONTINUE",
                user_text="",
                expected_end=False,
                expected_source_prefix="preview-empty",
            ),
            PreviewTestCase(
                name="太短（< 8 chars）→ CONTINUE",
                user_text="你好",
                expected_end=False,
                expected_source_prefix="partial-heuristic-too-short",
            ),
            PreviewTestCase(
                name="< 2 chars → CONTINUE（heuristic-short）",
                user_text="好",
                expected_end=False,
                expected_source_prefix="partial-heuristic-short",
            ),
            PreviewTestCase(
                name="continue suffix preview → CONTINUE",
                user_text="我想去然後",
                expected_end=False,
                expected_source_prefix="partial-heuristic-continue",
            ),
            PreviewTestCase(
                name="逗號結尾 partial → CONTINUE",
                user_text="我想說一些事情，",
                expected_end=False,
                expected_source_prefix="partial-heuristic-open-tail",
            ),
            PreviewTestCase(
                name="正常長句無特殊結尾 → preview-conservative（CONTINUE）",
                user_text="我今天做了一件事",
                expected_end=False,
                expected_source_prefix="preview-conservative",
            ),
        ]

        for pc in preview_cases:
            agent.reset()
            agent.push_user_text(pc.user_text)
            d = agent.preview_user_speak_end()
            self._check(pc.name, d, pc.expected_end, pc.expected_source_prefix)

    def run_merge_suite(self) -> None:
        """測試 push_user_text 的 merge 邏輯。"""
        print(f"\n{BOLD}[5] 文字合併邏輯（_merge_user_text）{RESET}")

        agent = self._make_agent(enabled=False, api_key="")

        def check_merge(name: str, pushes: list[tuple[str, bool]], expected_text: str) -> None:
            agent.reset()
            for text, is_final in pushes:
                agent.push_user_text(text, is_final=is_final)
            got = agent.current_user_text()
            ok = got == expected_text
            if ok:
                self._passed += 1
                print(f"  {PASS}  {name}")
            else:
                self._failed += 1
                print(f"  {FAIL}  {name}")
                print(f"       expected={expected_text!r} got={got!r}")

        # 第一次 push
        check_merge("首次 push", [("你好", False)], "你好")

        # incoming 是 current 的延伸（STT 累積）
        check_merge(
            "incoming startswith current → 取 incoming",
            [("你好", False), ("你好嗎", False)],
            "你好嗎",
        )

        # current 是 incoming 的延伸 → 保留 current
        check_merge(
            "current startswith incoming → 保留 current",
            [("你好嗎", False), ("你好", False)],
            "你好嗎",
        )

        # current 以 incoming 結尾 → 保留 current
        check_merge(
            "current endswith incoming → 保留 current",
            [("今天天氣不錯", False), ("不錯", False)],
            "今天天氣不錯",
        )

        # 完全不重疊 + current 以逗號結尾 → 直接串接（無 separator）
        check_merge(
            "不重疊且 current 以逗號結尾 → 無空格串接",
            [("我想說，", False), ("謝謝你", False)],
            "我想說，謝謝你",
        )

        # 完全不重疊 + current 無標點 → 加空格
        check_merge(
            "不重疊且無標點 → 加空格",
            [("hello", False), ("world", False)],
            "hello world",
        )

        # consume_user_text 重置後再 push
        agent.reset()
        agent.push_user_text("你好")
        consumed = agent.consume_user_text()
        agent.push_user_text("再次輸入")
        got = agent.current_user_text()
        ok = consumed == "你好" and got == "再次輸入"
        if ok:
            self._passed += 1
            print(f"  {PASS}  consume_user_text 後重新 push")
        else:
            self._failed += 1
            print(f"  {FAIL}  consume_user_text 後重新 push: consumed={consumed!r} next={got!r}")

    def run_dirty_cache_suite(self) -> None:
        """測試 _dirty 旗標與快取行為。"""
        print(f"\n{BOLD}[6] 快取（dirty flag）行為{RESET}")

        agent = self._make_agent(enabled=False, api_key="")

        # 第一次決策後，相同文字不應再次決策（快取命中）
        agent.reset()
        agent.push_user_text("你好嗎？")
        result1 = agent.is_user_speak_end()
        d1 = agent.last_decision()
        result2 = agent.is_user_speak_end()  # 應使用快取
        d2 = agent.last_decision()
        ok = result1 == result2 and d1 is d2
        if ok:
            self._passed += 1
            print(f"  {PASS}  相同文字第二次呼叫使用快取（same decision object）")
        else:
            self._failed += 1
            print(f"  {FAIL}  快取未命中 result1={result1} result2={result2} same_obj={d1 is d2}")

        # push 新文字後 dirty，應重新決策
        agent.push_user_text("今天天氣如何。")
        result3 = agent.is_user_speak_end()
        d3 = agent.last_decision()
        ok2 = d3 is not d1
        if ok2:
            self._passed += 1
            print(f"  {PASS}  push 新文字後重新決策（new decision object）")
        else:
            self._failed += 1
            print(f"  {FAIL}  push 新文字後應重新決策")

    def run_llm_suite(self) -> None:
        """測試真實 LLM 路徑（需 OPENAI_API_KEY 環境變數）。"""
        print(f"\n{BOLD}[7] LLM 路徑（需 OPENAI_API_KEY）{RESET}")

        if not self._api_key.strip():
            self._skipped += 3
            print(f"  {SKIP}  未設定 OPENAI_API_KEY，跳過 LLM 路徑測試")
            return

        agent = self._make_agent(enabled=True, api_key=self._api_key)

        # LLM cases: (name, text, expected_end, strict_end_check)
        # strict_end_check=False → 只驗證 source=llm，不嚴格斷言 end 值
        llm_cases = [
            # 噪字串，語意模糊，不嚴格斷言結果（只確認有呼叫 LLM）
            ("語意完整但 LLM 保守（只驗 source）", "我今天去了超市買了很多東西回來吃晚餐", None, False),
            # 語意明顯未完整的句子 → LLM 應回 CONTINUE
            ("明顯未結束的句子 → CONTINUE", "我覺得這件事情", False, True),
        ]

        for name, text, expected_end, strict in llm_cases:
            agent.reset()
            agent.push_user_text(text, is_final=False)
            agent.is_user_speak_end()
            d = agent.last_decision()
            assert d
            src_ok = d.source == "llm"
            if strict and expected_end is not None:
                end_ok = d.is_user_speak_end == expected_end
            else:
                end_ok = True  # 不嚴格驗 end
            ok = src_ok and end_ok
            if ok:
                self._passed += 1
            else:
                self._failed += 1
            note = ""
            if not src_ok:
                note += f"source={d.source!r}（expected 'llm'）; "
            if not end_ok:
                note += f"end={d.is_user_speak_end}（expected {expected_end}）"
            self._report(f"LLM: {name}", ok, d, note.strip())

        # LLM 被強制 enabled=False 時，即使有 key 也走 fallback
        agent2 = self._make_agent(enabled=False, api_key=self._api_key)
        agent2.push_user_text("我今天很累，想早點休息")
        agent2.is_user_speak_end()
        d2 = agent2.last_decision()
        assert d2
        src_ok2 = d2.source.startswith("fallback") or d2.source.startswith("heuristic") or d2.source.startswith("finalized")
        if src_ok2:
            self._passed += 1
            print(f"  {PASS}  enabled=False 即使有 key → 不呼叫 LLM（source={d2.source!r}）")
        else:
            self._failed += 1
            print(f"  {FAIL}  enabled=False 仍走 LLM（source={d2.source!r}）")

    # ─────────────────────────────────────────────────────────────────────────
    # Main entry
    # ─────────────────────────────────────────────────────────────────────────

    def run_all(self) -> None:
        banner = textwrap.dedent(f"""
        {BOLD}{'=' * 60}
        TurnAgent 決策實驗
        {'=' * 60}{RESET}
        """)
        print(banner)

        self.run_heuristic_suite()
        self.run_finalized_stt_suite()
        self.run_fallback_suite()
        self.run_preview_suite()
        self.run_merge_suite()
        self.run_dirty_cache_suite()
        self.run_llm_suite()

        total = self._passed + self._failed + self._skipped
        print(f"\n{BOLD}{'=' * 60}{RESET}")
        print(f"  結果：{PASS} {self._passed}  {FAIL} {self._failed}  {SKIP} {self._skipped}  / 共 {total}")
        print(f"{BOLD}{'=' * 60}{RESET}\n")

        if self._failed:
            sys.exit(1)


def main() -> None:
    api_key = os.environ.get("OPENAI_API_KEY", "")
    runner = ExperimentRunner(api_key=api_key)
    runner.run_all()


if __name__ == "__main__":
    main()
