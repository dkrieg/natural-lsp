* Task 8a fixture: perform-external/SHARED-SUB.NSS
* External subroutine definition (not inline).
* This is the target that PERFORM SHARED-SUB in MAIN.NSP should resolve to.

DEFINE SUBROUTINE SHARED-SUB
  DEFINE DATA
  LOCAL
    1 VALUE (A10)
  END DEFINE

  MOVE 'CALLED' TO VALUE

END SUBROUTINE
