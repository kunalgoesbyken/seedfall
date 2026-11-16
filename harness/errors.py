class StopRun(Exception):
    status = "stopped"


class BudgetExhausted(StopRun):
    status = "budget_exhausted"


class RetriesExhausted(StopRun):
    status = "provider_exhausted"


class FatalAPIError(StopRun):
    status = "fatal_api"


class MissingKey(Exception):
    pass


STOP_STATUSES = frozenset(cls.status for cls in StopRun.__subclasses__())
