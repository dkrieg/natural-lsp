* External subroutine fixture for hover Finding 1 (Story 2 AC1 / T4)
* Has a DEFINE DATA PARAMETER section so buildSubroutineHover renders a real
* parameter list (a scalar plus a group with children).

DEFINE DATA
PARAMETER
  1 IN-CODE (N3)
  1 OUT-DETAIL
    2 OUT-FLAG (L)
    2 OUT-TEXT (A20)
END DEFINE

DEFINE SUBROUTINE EXT-WORK
  WRITE 'external work'
END-SUBROUTINE

END
