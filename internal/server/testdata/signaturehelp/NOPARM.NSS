* External subroutine with NO DEFINE DATA PARAMETER block
* Only LOCAL data section (or none at all)
* Feature 17, T4a, Story 2 AC4 (empty-parameter case)
DEFINE DATA
LOCAL
  1 #LOCAL-VAR (A20)
END DEFINE

WRITE 'No parameters subroutine'

END
