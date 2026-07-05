* Task T13 fixture: external subroutine with mixed-case name
* Demonstrates name matching case-insensitively

DEFINE DATA
LOCAL
  1 HELPER-STRING (A100)
END DEFINE

DEFINE SUBROUTINE HelperFunc
  MOVE 'HELPER' TO HELPER-STRING
END-SUBROUTINE

DEFINE SUBROUTINE AnotherHelper
  MOVE 'ANOTHER' TO HELPER-STRING
END-SUBROUTINE

END
