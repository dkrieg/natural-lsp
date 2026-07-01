* Task 8a fixture: perform-inline-wins/SHARED-SUB.NSS
* External subroutine definition.
* PERFORM SHARED-SUB in MAIN.NSP should resolve to the INLINE
* DEFINE SUBROUTINE in MAIN.NSP, NOT to this external .NSS.
* M-4: inline definition wins. This file verifies the negative case.

DEFINE SUBROUTINE SHARED-SUB
  DEFINE DATA
  LOCAL
    1 VALUE (A10)
  END DEFINE

  MOVE 'EXTERNAL' TO VALUE

END SUBROUTINE
